package notification

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

type state struct {
	started, notified, cycle sql.NullInt64
	step                     int
}

func (s *Service) state(ctx context.Context, id int64) (state, error) {
	var value state
	err := s.db.QueryRowContext(ctx, `SELECT offline_started_at, offline_notified_at, traffic_cycle_started_at,
		traffic_last_notified_step FROM server_notification_state WHERE server_id = ?`, id).Scan(&value.started, &value.notified, &value.cycle, &value.step)
	if err == sql.ErrNoRows {
		err = nil
	}
	return value, err
}

func eligible(value serverstore.Server) bool {
	return value.Status != "pending" && value.LastSeenAt != nil && value.ArchivedAt == nil && value.DecommissioningAt == nil && value.DecommissionStatus == ""
}

func (s *Service) sweepOnline(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	servers, err := s.servers.List(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	offline, recovery := event{kind: "offline"}, event{kind: "recovery"}
	// Flush already consumed events even when a later row fails.
	defer func() { s.enqueue(offline); s.enqueue(recovery) }()
	seen := make(map[int64]bool, len(servers))
	for _, value := range servers {
		seen[value.ID] = true
		if !eligible(value) {
			delete(s.firstAbsent, value.ID)
			if _, err := s.db.ExecContext(ctx, `UPDATE server_notification_state SET offline_started_at = NULL, offline_notified_at = NULL, updated_at = ? WHERE server_id = ?`, now.Unix(), value.ID); err != nil {
				return err
			}
			continue
		}
		connected := s.agents.IsConnected(value.ID)
		previous, err := s.state(ctx, value.ID)
		if err != nil {
			return err
		}
		if connected {
			delete(s.firstAbsent, value.ID)
			if _, pending := s.pendingOffline[value.ID]; pending {
				continue
			}
			if previous.started.Valid {
				if _, err := s.db.ExecContext(ctx, `UPDATE server_notification_state SET offline_started_at = NULL, offline_notified_at = NULL, updated_at = ? WHERE server_id = ?`, now.Unix(), value.ID); err != nil {
					return err
				}
				if previous.notified.Valid && settings.configured() && settings.OnlineEnabled && settings.RecoveryEnabled {
					recovery.entries = append(recovery.entries, entry{server: value, startedAt: previous.started.Int64})
				}
			}
			continue
		}
		if !settings.configured() || !settings.OnlineEnabled {
			delete(s.firstAbsent, value.ID)
			continue
		}
		if previous.started.Valid {
			continue
		}
		first, exists := s.firstAbsent[value.ID]
		if !exists {
			s.firstAbsent[value.ID] = now
			continue
		}
		if now.Sub(first) < time.Duration(settings.OfflineGraceMinutes)*time.Minute || s.agents.IsConnected(value.ID) {
			continue
		}
		// started is the persisted generation marker; notified is set ONLY after
		// Telegram confirms delivery. Restart/failure cannot generate duplicates.
		if _, err := s.db.ExecContext(ctx, `INSERT INTO server_notification_state (server_id, offline_started_at, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(server_id) DO UPDATE SET offline_started_at = excluded.offline_started_at, offline_notified_at = NULL, updated_at = excluded.updated_at`, value.ID, first.Unix(), now.Unix()); err != nil {
			return err
		}
		offline.entries = append(offline.entries, entry{server: value, startedAt: first.Unix()})
		delete(s.firstAbsent, value.ID)
	}
	for id := range s.firstAbsent {
		if !seen[id] {
			delete(s.firstAbsent, id)
		}
	}
	return nil
}

func (s *Service) sweepTraffic(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil || !settings.configured() || !settings.TrafficEnabled {
		return err
	}
	servers, err := s.servers.List(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := event{kind: "traffic"}
	defer func() { s.enqueue(batch) }()
	for _, value := range servers {
		if !eligible(value) || value.MonthlyTrafficLimitBytes == nil || *value.MonthlyTrafficLimitBytes <= 0 || value.Metrics == nil || value.Metrics.CycleStartedAt == nil {
			continue
		}
		previous, err := s.state(ctx, value.ID)
		if err != nil {
			return err
		}
		cycle := value.Metrics.CycleStartedAt.Unix()
		step := trafficStep(value.TrafficUsedBytes(), *value.MonthlyTrafficLimitBytes, settings)
		last := previous.step
		if !previous.cycle.Valid || previous.cycle.Int64 != cycle {
			last = 0
		}
		// A correction below the initial threshold rearms this cycle. Merely
		// falling below a later step must not resend a previously consumed step.
		if step == 0 {
			last = 0
		}
		notify := step > last
		if notify {
			last = step
		}
		if !previous.cycle.Valid || previous.cycle.Int64 != cycle || last != previous.step {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO server_notification_state (server_id, traffic_cycle_started_at, traffic_last_notified_step, updated_at) VALUES (?, ?, ?, ?)
				ON CONFLICT(server_id) DO UPDATE SET traffic_cycle_started_at = excluded.traffic_cycle_started_at,
				traffic_last_notified_step = excluded.traffic_last_notified_step, updated_at = excluded.updated_at`, value.ID, cycle, last, s.now().Unix()); err != nil {
				return err
			}
		}
		if notify {
			batch.entries = append(batch.entries, entry{server: value, startedAt: cycle, step: step})
		}
	}
	return nil
}

func trafficStep(used, limit int64, settings Settings) int {
	if limit <= 0 {
		return 0
	}
	if used >= limit && settings.TrafficFullEnabled {
		return 100
	}
	step := 0
	for percent := settings.TrafficThresholdPercent; percent < 100; percent += settings.TrafficStepPercent {
		// Exact ceil(limit*percent/100), without overflowing int64.
		minimum := limit/100*int64(percent) + (limit%100*int64(percent)+99)/100
		if used < minimum {
			break
		}
		step = percent
	}
	return step
}

func message(kind string, entries []entry, now time.Time) string {
	title := map[string]string{"offline": "🔴 服务器离线", "recovery": "🟢 服务器恢复", "traffic": "⚠️ 服务器流量提醒"}[kind]
	lines := []string{title}
	shown := 0
	for _, value := range entries {
		if shown >= 20 {
			break
		}
		name := []rune(strings.Join(strings.Fields(value.server.Name), " "))
		if len(name) > 48 {
			name = append(name[:48], '…')
		}
		line := string(name)
		switch kind {
		case "offline":
			line += " 离线"
			if value.server.LastSeenAt != nil {
				line += "\n最后在线：" + value.server.LastSeenAt.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04:05") + " (UTC+8)"
			}
		case "recovery":
			seconds := max(int64(0), now.Unix()-value.startedAt)
			line += fmt.Sprintf(" 恢复在线\n离线时长：%d 分 %d 秒", seconds/60, seconds%60)
		case "traffic":
			used, limit := value.server.TrafficUsedBytes(), *value.server.MonthlyTrafficLimitBytes
			if value.step == 100 {
				line += " 🚨 流量已用尽"
			} else {
				line += " 流量提醒"
			}
			line += fmt.Sprintf("\n已使用：%.1f%%\n本周期：%s / %s", float64(used)/float64(limit)*100, formatBytes(used), formatBytes(limit))
		}
		// Leave room for the hidden-count suffix, counting surrogate pairs too.
		if len(utf16.Encode([]rune(strings.Join(lines, "\n\n")+"\n\n"+line))) > 3900 {
			break
		}
		lines = append(lines, line)
		shown++
	}
	if remaining := len(entries) - shown; remaining > 0 {
		lines = append(lines, fmt.Sprintf("……另外 %d 台", remaining))
	}
	return strings.Join(lines, "\n\n")
}

func formatBytes(value int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	scaled, index := float64(value), 0
	for scaled >= 1024 && index < len(units)-1 {
		scaled /= 1024
		index++
	}
	if index > 0 && scaled < 10 {
		return fmt.Sprintf("%.1f %s", scaled, units[index])
	}
	return fmt.Sprintf("%.0f %s", scaled, units[index])
}
