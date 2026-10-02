package notification

import (
	"errors"
	"regexp"
)

type Settings struct {
	TelegramTokenSet        bool   `json:"telegram_token_set"`
	TelegramChatID          string `json:"telegram_chat_id"`
	OnlineEnabled           bool   `json:"online_enabled"`
	OfflineGraceMinutes     int    `json:"offline_grace_minutes"`
	RecoveryEnabled         bool   `json:"recovery_enabled"`
	TrafficEnabled          bool   `json:"traffic_enabled"`
	TrafficThresholdPercent int    `json:"traffic_threshold_percent"`
	TrafficStepPercent      int    `json:"traffic_step_percent"`
	TrafficFullEnabled      bool   `json:"traffic_full_enabled"`
	botToken                string
}

type Update struct {
	Settings
	TelegramBotToken   string `json:"telegram_bot_token"`
	ClearTelegramToken bool   `json:"clear_telegram_token"`
}

type TestInput struct {
	TelegramBotToken string `json:"telegram_bot_token"`
	TelegramChatID   string `json:"telegram_chat_id"`
}

type TestResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

var (
	ErrInvalidSettings = errors.New("通知设置无效")
	ErrTestBusy        = errors.New("测试消息正在发送，请稍后再试")
	ErrQueueFull       = errors.New("通知队列已满，请稍后再试")
	ErrTestNotFound    = errors.New("测试结果不存在或已过期")
	tokenPattern       = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
	chatPattern        = regexp.MustCompile(`^-?[0-9]+$`)
)

func validToken(value string) bool { return len(value) <= 256 && tokenPattern.MatchString(value) }
func validChat(value string) bool  { return len(value) <= 32 && chatPattern.MatchString(value) }

func (s Settings) configured() bool { return s.botToken != "" && s.TelegramChatID != "" }

func (u Update) valid() bool {
	return (u.TelegramBotToken == "" || validToken(u.TelegramBotToken)) &&
		(u.TelegramChatID == "" || validChat(u.TelegramChatID)) &&
		!(u.ClearTelegramToken && u.TelegramBotToken != "") &&
		u.OfflineGraceMinutes >= 1 && u.OfflineGraceMinutes <= 60 &&
		u.TrafficThresholdPercent >= 50 && u.TrafficThresholdPercent <= 100 &&
		(u.TrafficStepPercent == 5 || u.TrafficStepPercent == 10 || u.TrafficStepPercent == 20)
}
