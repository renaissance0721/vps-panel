package notification

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

type event struct {
	kind      string
	entries   []entry
	testID    string
	testInput TestInput
}

type entry struct {
	server    serverstore.Server
	startedAt int64
	step      int
}

type Service struct {
	db             *sql.DB
	servers        *serverstore.Service
	agents         *agentcontrol.Service
	sender         *telegramSender
	now            func() time.Time
	retryDelay     time.Duration
	queue          chan event
	mu             sync.Mutex
	firstAbsent    map[int64]time.Time
	pendingOffline map[int64]int64
	test           TestResult
}

func NewService(db *sql.DB, servers *serverstore.Service, agents *agentcontrol.Service) *Service {
	return &Service{db: db, servers: servers, agents: agents, sender: newTelegramSender(), now: time.Now,
		retryDelay: 10 * time.Second, queue: make(chan event, 64), firstAbsent: make(map[int64]time.Time), pendingOffline: make(map[int64]int64)}
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var value Settings
	err := s.db.QueryRowContext(ctx, `SELECT telegram_bot_token, telegram_chat_id, online_enabled, offline_grace_minutes,
		recovery_enabled, traffic_enabled, traffic_threshold_percent, traffic_step_percent, traffic_full_enabled
		FROM notification_settings WHERE id = 1`).Scan(&value.botToken, &value.TelegramChatID, &value.OnlineEnabled,
		&value.OfflineGraceMinutes, &value.RecoveryEnabled, &value.TrafficEnabled, &value.TrafficThresholdPercent,
		&value.TrafficStepPercent, &value.TrafficFullEnabled)
	value.TelegramTokenSet = value.botToken != ""
	return value, err
}

func (s *Service) Save(ctx context.Context, value Update) error {
	if !value.valid() {
		return ErrInvalidSettings
	}
	_, err := s.db.ExecContext(ctx, `UPDATE notification_settings SET
		telegram_bot_token = CASE WHEN ? THEN '' WHEN ? <> '' THEN ? ELSE telegram_bot_token END,
		telegram_chat_id = ?, online_enabled = ?, offline_grace_minutes = ?, recovery_enabled = ?, traffic_enabled = ?,
		traffic_threshold_percent = ?, traffic_step_percent = ?, traffic_full_enabled = ?, updated_at = ? WHERE id = 1`,
		value.ClearTelegramToken, value.TelegramBotToken, value.TelegramBotToken, value.TelegramChatID,
		value.OnlineEnabled, value.OfflineGraceMinutes, value.RecoveryEnabled, value.TrafficEnabled,
		value.TrafficThresholdPercent, value.TrafficStepPercent, value.TrafficFullEnabled, s.now().Unix())
	return err
}

func (s *Service) SendTest(ctx context.Context, input TestInput) (TestResult, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return TestResult{}, err
	}
	if input.TelegramBotToken == "" {
		input.TelegramBotToken = settings.botToken
	}
	if !validToken(input.TelegramBotToken) || !validChat(input.TelegramChatID) {
		return TestResult{}, ErrInvalidSettings
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.test.Status == "pending" {
		return TestResult{}, ErrTestBusy
	}
	id, _, err := token.New()
	if err != nil {
		return TestResult{}, err
	}
	result := TestResult{ID: id, Status: "pending"}
	select {
	case s.queue <- event{kind: "test", testID: id, testInput: input}:
		s.test = result
		return result, nil
	default:
		return TestResult{}, ErrQueueFull
	}
}

func (s *Service) TestResult(id string) (TestResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || s.test.ID != id {
		return TestResult{}, ErrTestNotFound
	}
	return s.test, nil
}

// Run owns the single sender and both sweep schedules. Cancellation interrupts
// requests and retry waits, and Run waits for the worker before returning.
func (s *Service) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() { defer close(done); s.work(ctx) }()
	defer func() { <-done }()
	online, traffic := time.NewTicker(30*time.Second), time.NewTicker(60*time.Second)
	defer online.Stop()
	defer traffic.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-online.C:
			if err := s.sweepOnline(ctx); err != nil && ctx.Err() == nil {
				log.Printf("notification online sweep failed: %v", err)
			}
		case <-traffic.C:
			if err := s.sweepTraffic(ctx); err != nil && ctx.Err() == nil {
				log.Printf("notification traffic sweep failed: %v", err)
			}
		}
	}
}

func (s *Service) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-s.queue:
			s.deliver(ctx, item)
		}
	}
}

func (s *Service) deliver(ctx context.Context, item event) {
	var err error
	var sent []entry
	for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
		settings, readErr := s.Settings(ctx)
		if readErr != nil {
			err = errors.New("无法读取通知设置")
			break
		}
		botToken, chatID := settings.botToken, settings.TelegramChatID
		text := "✅ VPS Panel Telegram 测试通知\n通知配置有效。"
		if item.kind == "test" {
			botToken, chatID = item.testInput.TelegramBotToken, item.testInput.TelegramChatID
		} else {
			if !settings.configured() || (item.kind == "offline" && !settings.OnlineEnabled) ||
				(item.kind == "recovery" && (!settings.OnlineEnabled || !settings.RecoveryEnabled)) || (item.kind == "traffic" && !settings.TrafficEnabled) {
				break
			}
			sent, err = s.currentEntries(ctx, item, settings)
			if err != nil || len(sent) == 0 {
				break
			}
			text = message(item.kind, sent, s.now())
		}
		err = s.sender.send(ctx, botToken, chatID, text)
		if err == nil {
			break
		}
		sent = nil
		var failure *sendError
		if !errors.As(err, &failure) || !failure.retry || attempt == 2 {
			break
		}
		delay := max(s.retryDelay, failure.retryAfter)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.kind == "offline" {
		if err == nil {
			for _, value := range sent {
				if _, updateErr := s.db.ExecContext(ctx, `UPDATE server_notification_state SET offline_notified_at = ?, updated_at = ?
					WHERE server_id = ? AND offline_started_at = ?`, s.now().Unix(), s.now().Unix(), value.server.ID, value.startedAt); updateErr != nil {
					log.Printf("notification delivery state failed: %v", updateErr)
				}
			}
		}
		for _, value := range item.entries {
			delete(s.pendingOffline, value.server.ID)
		}
	}
	if item.kind == "test" && s.test.ID == item.testID {
		s.test.Status = "success"
		if err != nil {
			s.test.Status, s.test.Error = "error", err.Error()
		}
	}
	if err != nil {
		log.Printf("notification %s delivery failed: %v", item.kind, err)
	}
}

// Never keep a database transaction/Rows open while consulting the connection
// map: agent reports hold its mutex while writing to the same SQLite database.
func (s *Service) currentEntries(ctx context.Context, item event, settings Settings) ([]entry, error) {
	var result []entry
	for _, value := range item.entries {
		current, err := s.servers.Get(ctx, value.server.ID)
		if errors.Is(err, serverstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, errors.New("无法读取服务器通知状态")
		}
		if !eligible(current) {
			continue
		}
		connected := s.agents.IsConnected(current.ID)
		if (item.kind == "offline" && connected) || (item.kind == "recovery" && !connected) {
			continue
		}
		if item.kind == "traffic" {
			if current.Metrics == nil || current.Metrics.CycleStartedAt == nil || current.Metrics.CycleStartedAt.Unix() != value.startedAt ||
				current.MonthlyTrafficLimitBytes == nil || *current.MonthlyTrafficLimitBytes <= 0 ||
				trafficStep(current.TrafficUsedBytes(), *current.MonthlyTrafficLimitBytes, settings) < value.step {
				continue
			}
		}
		value.server = current
		result = append(result, value)
	}
	return result, nil
}

// Called with mu held. Events are persisted before enqueue, so queue overflow
// and delivery failures cannot recreate the same event on every sweep.
func (s *Service) enqueue(item event) {
	if len(item.entries) == 0 {
		return
	}
	select {
	case s.queue <- item:
		if item.kind == "offline" {
			for _, value := range item.entries {
				s.pendingOffline[value.server.ID] = value.startedAt
			}
		}
	default:
		log.Printf("notification queue full; dropped %s batch (%d servers)", item.kind, len(item.entries))
	}
}
