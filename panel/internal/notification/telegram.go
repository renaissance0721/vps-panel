package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

type telegramSender struct {
	client  *http.Client
	baseURL string
}

// Never retain the transport error, request URL or Telegram description: all
// three may contain the bot credential. Only these local messages leave sender.
type sendError struct {
	message    string
	retry      bool
	retryAfter time.Duration
}

func (e *sendError) Error() string { return e.message }

func newTelegramSender() *telegramSender {
	return &telegramSender{
		client:  &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		baseURL: "https://api.telegram.org",
	}
}

func (s *telegramSender) send(ctx context.Context, botToken, chatID, text string) error {
	body, _ := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/bot"+botToken+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return &sendError{message: "Telegram 请求配置无效"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return &sendError{message: "Telegram 请求超时", retry: true}
		}
		return &sendError{message: "无法连接 Telegram，请检查 Panel 网络", retry: true}
	}
	defer resp.Body.Close()
	var result struct {
		OK         bool `json:"ok"`
		ErrorCode  int  `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result)
	if resp.StatusCode == http.StatusOK && decodeErr == nil && result.OK {
		return nil
	}
	code := resp.StatusCode
	if code == http.StatusOK {
		code = result.ErrorCode
	}
	failure := &sendError{message: "Telegram 返回无效响应"}
	switch {
	case code == 401 || code == 404:
		failure.message = "Telegram Bot Token 无效"
	case code == 400:
		failure.message = "Chat ID 无效，或 Bot 尚未加入聊天；请先向 Bot 发送消息"
	case code == 403:
		failure.message = "Bot 没有发送权限或已被屏蔽"
	case code == 429:
		failure.message, failure.retry = "Telegram 请求过于频繁，请稍后重试", true
		if result.Parameters.RetryAfter > 0 && result.Parameters.RetryAfter <= 86400 {
			failure.retryAfter = time.Duration(result.Parameters.RetryAfter) * time.Second
		}
	case code >= 500 && code <= 599:
		failure.message, failure.retry = "Telegram 服务暂时不可用", true
	case code >= 300 && code <= 399:
		failure.message = "Telegram 返回了不允许的重定向"
	}
	return failure
}
