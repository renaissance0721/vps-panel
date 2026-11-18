package localconnector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/renaissance0721/vps-panel/panel/internal/subscriptionfetch"
)

var (
	errPanelURL   = errors.New("请输入 Panel 的 HTTPS 根地址，例如 https://panel.example.com")
	errPanelHTTPS = errors.New("Panel 拒绝连接，请确认 HTTPS 与反向代理配置")
	errPairCode   = errors.New("配对码无效、已使用或已过期，请在 Panel 重新生成")
	errCredential = errors.New("连接器凭据已失效，请在 Panel 重新配对")
)

type Client struct {
	http  *http.Client
	fetch func(context.Context, string, string) (subscriptionfetch.Result, error)
}

func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:            (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
				TLSHandshakeTimeout:    8 * time.Second,
				MaxResponseHeaderBytes: 32 << 10,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		fetch: subscriptionfetch.New().Fetch,
	}
}

func PanelURL(address string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || u.User != nil ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errPanelURL
	}
	return "https://" + u.Host, nil
}

func (c *Client) Pair(ctx context.Context, address, code string) (string, error) {
	base, err := PanelURL(address)
	if err != nil {
		return "", err
	}
	if !ValidToken(code) {
		return "", errPairCode
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PairPath, nil)
	if err != nil {
		return "", errPanelURL
	}
	request.Header.Set("Authorization", "Bearer "+code)
	response, err := c.http.Do(request)
	if err != nil {
		return "", errors.New("无法通过 HTTPS 连接 Panel，请检查地址、证书和网络")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return "", errPairCode
	case http.StatusForbidden:
		return "", errPanelHTTPS
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
		var result PairResponse
		if err == nil && len(body) <= 4096 && json.Unmarshal(body, &result) == nil && ValidToken(result.Credential) {
			return result.Credential, nil
		}
	}
	return "", errors.New("Panel 配对响应无效，请确认服务已支持本地连接器")
}

func (c *Client) Run(ctx context.Context, address, credential string, report func(string)) error {
	base, err := PanelURL(address)
	if err != nil {
		return err
	}
	if !ValidToken(credential) {
		return errCredential
	}
	endpoint := "wss://" + strings.TrimPrefix(base, "https://") + SocketPath
	delay := time.Second
	report("正在连接 Panel…")
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		socket, response, err := websocket.Dial(dialCtx, endpoint, &websocket.DialOptions{
			HTTPClient: c.http, HTTPHeader: http.Header{"Authorization": {"Bearer " + credential}},
		})
		cancel()
		if err == nil {
			delay = time.Second
			report("已连接，等待 Panel 请求。")
			c.serve(ctx, socket)
			if ctx.Err() == nil {
				report("连接已断开，正在重连…")
			}
		} else if response != nil {
			switch response.StatusCode {
			case http.StatusUnauthorized:
				return errCredential
			case http.StatusForbidden:
				return errPanelHTTPS
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
	return nil
}

func (c *Client) serve(ctx context.Context, socket *websocket.Conn) {
	defer socket.CloseNow()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	socket.SetReadLimit(TaskReadLimit)
	tasks := make(chan Task)
	go func() {
		defer cancel()
		for {
			var task Task
			if wsjson.Read(ctx, socket, &task) != nil || !ValidToken(task.ID) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case tasks <- task:
			}
		}
	}()
	go func() {
		defer cancel()
		KeepAlive(ctx, socket, nil)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-tasks:
			result := c.download(ctx, task)
			writeCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			err := wsjson.Write(writeCtx, socket, result)
			stop()
			if err != nil {
				return
			}
		}
	}
}

func (c *Client) download(ctx context.Context, task Task) Result {
	fetched, err := c.fetch(ctx, task.URL, task.UserAgent)
	result := Result{ID: task.ID, Status: fetched.Status}
	switch {
	case err == nil:
		result.Body = fetched.Body
		result.SubscriptionInfo = safeMetadata(fetched.SubscriptionInfo)
	case errors.Is(err, subscriptionfetch.ErrURL):
		result.Error = Blocked
	case errors.Is(err, subscriptionfetch.ErrTimeout):
		result.Error = Timeout
	case errors.Is(err, subscriptionfetch.ErrStatus):
		result.Error = HTTPError
	case errors.Is(err, subscriptionfetch.ErrTooLarge):
		result.Error = TooLarge
	default:
		result.Error = Unreachable
	}
	return result
}

func (c *Client) Diagnose(ctx context.Context) (netip.Addr, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// This explicit CLI operation sends only a request to the fixed diagnostic site.
	result, err := c.fetch(ctx, "https://api.country.is/", "")
	if err != nil {
		return netip.Addr{}, "", errors.New("无法查询公网出口，请检查 DNS、直连规则和网络")
	}
	var value struct {
		IP      netip.Addr `json:"ip"`
		Country string     `json:"country"`
	}
	if len(result.Body) > 4096 || json.Unmarshal(result.Body, &value) != nil || !subscriptionfetch.PublicIP(value.IP) ||
		len(value.Country) != 2 || value.Country[0] < 'A' || value.Country[0] > 'Z' || value.Country[1] < 'A' || value.Country[1] > 'Z' {
		return netip.Addr{}, "", errors.New("出口诊断响应无效")
	}
	return value.IP, value.Country, nil
}
