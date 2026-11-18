package localconnector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/subscriptionfetch"
)

const (
	PairPath        = "/api/local-connector/pair"
	SocketPath      = "/api/local-connector/connect"
	TaskReadLimit   = 32 << 10
	ResultReadLimit = (subscriptionfetch.MaxBytes+2)/3*4 + 4096
	MetadataLimit   = 128
)

type Task struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	UserAgent string `json:"user_agent"`
}

type Result struct {
	ID               string `json:"id"`
	Status           int    `json:"status,omitempty"`
	Error            string `json:"error,omitempty"`
	Body             []byte `json:"body,omitempty"`
	SubscriptionInfo string `json:"subscription_info,omitempty"`
}

const (
	Blocked     = "blocked"
	Unreachable = "unreachable"
	Timeout     = "timeout"
	HTTPError   = "http_status"
	TooLarge    = "too_large"
)

type PairResponse struct {
	Credential string `json:"credential"`
}

func ValidToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Export only the numeric fields recognized by the subscription cache.
func safeMetadata(header string) string {
	upload, download, total, expire := subscriptionfetch.Metadata(header)
	var safe strings.Builder
	for _, field := range []struct {
		name  string
		value *int64
	}{{"upload", upload}, {"download", download}, {"total", total}, {"expire", expire}} {
		if field.value != nil {
			fmt.Fprintf(&safe, "%s=%d;", field.name, *field.value)
		}
	}
	return safe.String()
}

// Ping requires a concurrent WebSocket reader to process the pong.
func KeepAlive(ctx context.Context, connection *websocket.Conn, onPong func()) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := connection.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
			if onPong != nil {
				onPong()
			}
		}
	}
}
