package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

const (
	panelDiagnosticTimeout = 12 * time.Second
	panelProbeTimeout      = 2 * time.Second
	maxPanelProbes         = 8
)

type serverDiagnosticResponse struct {
	ServerID   int64              `json:"server_id"`
	StartedAt  time.Time          `json:"started_at"`
	DurationMS int64              `json:"duration_ms"`
	Checks     []diagnostic.Check `json:"checks"`
}

type panelEntry struct {
	resourceID int64
	label      string
	host       string
	port       int
	protocol   string
}

func (s *server) diagnoseServer(w http.ResponseWriter, r *http.Request, user auth.User) {
	serverID, ok := readPositiveID(w, r.PathValue("id"), "服务器 ID 无效")
	if !ok {
		return
	}
	if !s.requireServerAccess(w, r, user, serverID) {
		return
	}
	startedAt := time.Now().UTC()
	status, err := s.agents.GetDiagnosticStatus(r.Context(), serverID)
	if err != nil {
		writeServerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), panelDiagnosticTimeout)
	defer cancel()
	result, err := s.agents.RequestDiagnostics(ctx, serverID)
	if err != nil {
		writeDiagnosticError(w, err)
		return
	}

	proxies, err := s.proxies.List(ctx)
	if err != nil {
		writeInternalError(w)
		return
	}
	relays, err := s.relays.List(ctx)
	if err != nil {
		writeInternalError(w)
		return
	}
	proxyNames, relayNames, entries, err := s.diagnosticResources(ctx, user, serverID, proxies, relays)
	if err != nil {
		writeInternalError(w)
		return
	}

	checks := []diagnostic.Check{
		{Code: "agent.connected", Status: diagnostic.StatusPass, Detail: "Agent 在线并已响应诊断请求"},
		diagnosticVersionCheck(status),
		diagnosticSyncCheck(status),
	}
	checks = append(checks, filterAgentDiagnosticChecks(result.Checks, proxyNames, relayNames)...)
	checks = append(checks, probePanelEntries(ctx, entries, dialPanelEntry)...)
	checks = limitDiagnosticChecks(checks, diagnostic.MaxChecks-1)
	checks = append(checks, diagnostic.Check{
		Code: "protocol.end_to_end", Status: diagnostic.StatusSkipped,
		Detail: "未执行 VLESS / Shadowsocks 协议端到端握手；TCP 可达不等于协议可用",
	})
	writeJSON(w, http.StatusOK, serverDiagnosticResponse{
		ServerID: serverID, StartedAt: startedAt,
		DurationMS: time.Since(startedAt).Milliseconds(), Checks: checks,
	})
}

func (s *server) diagnosticResources(
	ctx context.Context,
	user auth.User,
	serverID int64,
	proxies []proxystore.Proxy,
	relays []relaystore.Relay,
) (map[int64]string, map[int64]string, []panelEntry, error) {
	proxyNames := make(map[int64]string)
	relayNames := make(map[int64]string)
	entries := make([]panelEntry, 0)
	for _, proxy := range proxies {
		if proxy.ServerID != serverID || !proxy.Enabled {
			continue
		}
		proxyNames[proxy.ID] = proxy.Name
		entries = append(entries, panelEntry{
			resourceID: proxy.ID, label: proxy.Name, host: proxy.EntryAddress,
			port: proxy.ListenPort, protocol: "tcp",
		})
	}
	for _, relay := range relays {
		if relay.ServerID != serverID || !relay.Enabled {
			continue
		}
		if _, err := s.relayForUser(ctx, user, relay.ID); err != nil {
			if errors.Is(err, relaystore.ErrNotFound) {
				continue
			}
			return nil, nil, nil, err
		}
		relayNames[relay.ID] = relay.Name
		entry := panelEntry{
			resourceID: relay.ID, label: relay.Name, host: relay.EntryAddress,
			port: relay.ListenPort, protocol: "tcp",
		}
		if relay.Network == relaystore.NetworkUDP {
			entry.protocol = "udp"
		}
		entries = append(entries, entry)
	}
	return proxyNames, relayNames, entries, nil
}

func diagnosticVersionCheck(status agentcontrol.DiagnosticStatus) diagnostic.Check {
	check := diagnostic.Check{
		Code: "config.version", Status: diagnostic.StatusPass,
		Detail: fmt.Sprintf("Desired v%d / Applied v%d", status.DesiredVersion, status.AppliedVersion),
	}
	if status.DesiredVersion != status.AppliedVersion {
		check.Status = diagnostic.StatusFail
		message := "当前运行配置与 Panel desired state 不一致"
		if status.DesiredVersion > status.AppliedVersion {
			message = "当前运行配置落后于 Panel desired state"
		}
		check.Detail = fmt.Sprintf("%s（Desired v%d / Applied v%d）", message, status.DesiredVersion, status.AppliedVersion)
	}
	return check
}

func diagnosticSyncCheck(status agentcontrol.DiagnosticStatus) diagnostic.Check {
	check := diagnostic.Check{Code: "config.sync", Status: diagnostic.StatusPass, Detail: "最近一次配置同步成功"}
	switch status.SyncStatus {
	case agentcontrol.ConfigSyncSuccess:
	case agentcontrol.ConfigSyncPending:
		check.Status = diagnostic.StatusWarning
		check.Detail = "配置同步正在等待或进行中"
	case agentcontrol.ConfigSyncFailed:
		check.Status = diagnostic.StatusFail
		check.Detail = "最近一次配置同步失败"
		if message := diagnostic.SafeText(status.SyncError, diagnostic.MaxDetailBytes); message != "" {
			check.Detail = diagnostic.SafeText(check.Detail+"："+message, diagnostic.MaxDetailBytes)
		}
	default:
		check.Status = diagnostic.StatusWarning
		check.Detail = "最近一次配置同步状态未知"
	}
	return check
}

func filterAgentDiagnosticChecks(
	checks []diagnostic.Check,
	proxyNames map[int64]string,
	relayNames map[int64]string,
) []diagnostic.Check {
	filtered := make([]diagnostic.Check, 0, len(checks))
	for _, check := range checks {
		var name string
		switch check.Code {
		case "xray.service", "xray.config", "realm.service", "diagnostic.truncated":
			if check.ResourceID == nil {
				filtered = append(filtered, check)
			}
			continue
		case "xray.listener", "tls.certificate":
			if check.ResourceID == nil {
				continue
			}
			name = proxyNames[*check.ResourceID]
		case "realm.listener", "relay.dns", "relay.target_tcp":
			if check.ResourceID == nil {
				continue
			}
			name = relayNames[*check.ResourceID]
		default:
			continue
		}
		if name == "" {
			continue
		}
		check.Label = name
		filtered = append(filtered, check)
	}
	return filtered
}

func probePanelEntries(
	ctx context.Context,
	entries []panelEntry,
	dial func(context.Context, string) (net.Conn, error),
) []diagnostic.Check {
	if len(entries) == 0 {
		return nil
	}
	type job struct {
		index int
		entry panelEntry
	}
	jobs := make(chan job)
	checks := make([]diagnostic.Check, len(entries))
	workers := min(maxPanelProbes, len(entries))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for value := range jobs {
				checks[value.index] = probePanelEntry(ctx, value.entry, dial)
			}
		}()
	}
	for index, entry := range entries {
		jobs <- job{index: index, entry: entry}
	}
	close(jobs)
	group.Wait()
	return checks
}

func probePanelEntry(ctx context.Context, entry panelEntry, dial func(context.Context, string) (net.Conn, error)) diagnostic.Check {
	id := entry.resourceID
	check := diagnostic.Check{
		Code: "panel.entry_tcp", ResourceID: &id, Label: entry.label,
		Protocol: entry.protocol,
	}
	if entry.protocol != "tcp" {
		check.Status = diagnostic.StatusSkipped
		check.Detail = "Relay 仅启用 UDP，Panel 未执行远端 UDP 可达性检测"
		return check
	}
	if entry.host == "" {
		check.Status = diagnostic.StatusSkipped
		check.Detail = "节点公网入口地址不可用"
		return check
	}
	check.Endpoint = net.JoinHostPort(strings.Trim(entry.host, "[]"), strconv.Itoa(entry.port))
	probeContext, cancel := context.WithTimeout(ctx, panelProbeTimeout)
	startedAt := time.Now()
	connection, err := dial(probeContext, check.Endpoint)
	latency := time.Since(startedAt).Milliseconds()
	cancel()
	if err != nil {
		check.Status = diagnostic.StatusFail
		check.Detail = safePanelProbeError(err)
		return check
	}
	_ = connection.Close()
	check.Status = diagnostic.StatusPass
	check.LatencyMS = &latency
	check.Detail = "Panel 所在网络可以建立节点入口 TCP 连接"
	return check
}

func dialPanelEntry(ctx context.Context, endpoint string) (net.Conn, error) {
	return (&net.Dialer{Timeout: panelProbeTimeout}).DialContext(ctx, "tcp", endpoint)
}

func safePanelProbeError(err error) string {
	message := err.Error()
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(message), "timeout") {
		message = "timeout"
	}
	return diagnostic.SafeText("Panel 所在网络无法建立节点入口 TCP 连接: "+message, diagnostic.MaxDetailBytes)
}

func limitDiagnosticChecks(checks []diagnostic.Check, maximum int) []diagnostic.Check {
	if len(checks) <= maximum {
		return checks
	}
	return append(checks[:maximum-1], diagnostic.Check{
		Code: "diagnostic.truncated", Status: diagnostic.StatusWarning,
		Detail: "诊断项目过多，完整报告已按安全限制截断",
	})
}

func writeDiagnosticError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentcontrol.ErrAgentOffline):
		writeError(w, http.StatusConflict, "Agent 当前不在线")
	case errors.Is(err, agentcontrol.ErrDiagnosticsUnsupported):
		writeError(w, http.StatusConflict, "当前 Agent 不支持一键诊断，请升级 Agent。")
	case errors.Is(err, agentcontrol.ErrDiagnosticsInProgress):
		writeError(w, http.StatusConflict, "该服务器正在诊断，请稍后重试")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "服务器诊断超时，请稍后重试")
	case errors.Is(err, context.Canceled):
		return
	default:
		writeInternalError(w)
	}
}
