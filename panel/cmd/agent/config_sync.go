package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
)

const unsupportedManagedConfigMessage = "managed proxy configuration is not supported by this Agent version"

var (
	errUnsupportedManagedConfig = errors.New(unsupportedManagedConfigMessage)
	errManagedRuntimePurge      = errors.New("managed runtime purge failed")
	errAgentSelfUninstallLaunch = errors.New("Agent self-uninstall launch failed")
)

const (
	configRequestTimeout = 10 * time.Second
	configApplyTimeout   = 8 * time.Minute
)

type desiredState struct {
	Version                 int64                `json:"version"`
	ComponentResults        bool                 `json:"component_results"`
	Hysteria2HoppingControl bool                 `json:"hysteria2_hopping_control"`
	Decommission            bool                 `json:"decommission"`
	BlockChinaInbound       bool                 `json:"block_china_inbound"`
	ChinaInboundWhitelist   []string             `json:"china_inbound_whitelist"`
	Xray                    desiredXrayState     `json:"xray"`
	Singbox                 *desiredSingboxState `json:"singbox,omitempty"`
	Realm                   desiredRealmState    `json:"realm"`
}

type desiredSingboxState struct {
	Enabled bool           `json:"enabled"`
	Purge   bool           `json:"purge"`
	Proxies []desiredProxy `json:"proxies"`
}

type desiredXrayState struct {
	Enabled            bool           `json:"enabled"`
	Purge              bool           `json:"purge"`
	OutboundPreference string         `json:"outbound_preference"`
	Proxies            []desiredProxy `json:"proxies"`
}

type desiredProxy struct {
	ID          int64               `json:"id"`
	Listen      string              `json:"listen"`
	Port        int                 `json:"port"`
	Protocol    string              `json:"protocol"`
	Transport   string              `json:"transport,omitempty"`
	Security    string              `json:"security,omitempty"`
	ServerFlow  string              `json:"server_flow,omitempty"`
	ServerName  string              `json:"server_name,omitempty"`
	TLS         *desiredTLS         `json:"tls,omitempty"`
	Reality     *desiredReality     `json:"reality,omitempty"`
	Shadowsocks *desiredShadowsocks `json:"shadowsocks,omitempty"`
	Hysteria2   *desiredHysteria2   `json:"hysteria2,omitempty"`
	Clients     []desiredClient     `json:"clients"`
}

type desiredHysteria2 struct {
	UpMbps             int    `json:"up_mbps,omitempty"`
	DownMbps           int    `json:"down_mbps,omitempty"`
	ObfsPassword       string `json:"obfs_password,omitempty"`
	PortHoppingEnabled bool   `json:"port_hopping_enabled,omitempty"`
	PortHoppingStart   int    `json:"port_hopping_start,omitempty"`
	PortHoppingEnd     int    `json:"port_hopping_end,omitempty"`
}

type desiredShadowsocks struct {
	Method   string `json:"method"`
	Network  string `json:"network"`
	Password string `json:"password"`
}

type desiredTLS struct {
	Mode        string `json:"mode"`
	Certificate string `json:"certificate,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
}

type desiredReality struct {
	Target     string `json:"target"`
	PrivateKey string `json:"private_key"`
	ShortID    string `json:"short_id"`
}

type desiredClient struct {
	ID       int64  `json:"id"`
	StatsID  string `json:"stats_id"`
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
}

type desiredRealmState struct {
	Enabled bool           `json:"enabled"`
	Purge   bool           `json:"purge"`
	Relays  []desiredRelay `json:"relays"`
}

type desiredRelay struct {
	ID            int64  `json:"id"`
	ListenAddress string `json:"listen_address"`
	ListenPort    int    `json:"listen_port"`
	TargetHost    string `json:"target_host"`
	TargetPort    int    `json:"target_port"`
	Network       string `json:"network"`
}

type configResult struct {
	Version    int64             `json:"version"`
	Status     string            `json:"status"`
	Message    string            `json:"message"`
	Components map[string]string `json:"components,omitempty"`
}

type configSynchronizer struct {
	config                config
	client                *http.Client
	applyState            func(context.Context, desiredState) error
	applyDecommission     func(context.Context, desiredState) error
	prepareSelfUninstall  func() (*preparedSelfUninstall, error)
	renewCertificates     func(context.Context) error
	refreshHopping        func(context.Context, desiredState) error
	diagnoseState         func(context.Context, desiredState) []diagnostic.Check
	chinaFirewall         *chinaInboundFirewall
	now                   func() time.Time
	mu                    sync.Mutex
	lastSuccessfulVersion int64
	lastSuccessfulState   desiredState
	hasSuccessfulState    bool
	firewallRetryVersion  int64
	firewallRetryAt       time.Time
	firewallRetryDelay    time.Duration
	components            map[string]string
}

func newConfigSynchronizer(value config, client *http.Client) *configSynchronizer {
	xray := newXrayManager()
	realm := newRealmManager()
	chinaFirewall := newChinaInboundFirewall()
	acme := xray.acme
	singboxACME := newACMEManager()
	singboxACME.certDir = managedSingboxACMECertDir
	singboxACME.homeDir = "/var/lib/vps-panel/acme-singbox"
	singbox := newSingboxManager(singboxACME)
	hopping := newHY2HoppingManager()
	runner := newAgentDiagnosticRunner(xray, realm)
	var synchronizer *configSynchronizer
	synchronizer = &configSynchronizer{config: value, client: client, renewCertificates: func(ctx context.Context) error {
		return errors.Join(xray.renewCertificates(ctx), singbox.renewCertificates(ctx))
	}, refreshHopping: func(ctx context.Context, state desiredState) error {
		var proxies []desiredProxy
		if state.Singbox != nil {
			proxies = state.Singbox.Proxies
		}
		rules, err := hy2HoppingRules(proxies)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			for _, proxy := range state.Xray.Proxies {
				if proxy.Port >= rule.start && proxy.Port <= rule.end {
					return errHoppingConflict
				}
			}
			for _, relay := range state.Realm.Relays {
				if relay.ListenPort >= rule.start && relay.ListenPort <= rule.end {
					return errHoppingConflict
				}
			}
		}
		return hopping.apply(ctx, rules)
	}, diagnoseState: runner.run, chinaFirewall: chinaFirewall, now: time.Now, prepareSelfUninstall: prepareAgentSelfUninstall, applyState: func(ctx context.Context, state desiredState) error {
		var err error
		synchronizer.components, err = applyManagedComponents(ctx, state, xray.apply, realm.apply, chinaFirewall)
		return errors.Join(err, applySingboxAndHopping(ctx, state, singbox, synchronizer.refreshHopping, synchronizer.components))
	}, applyDecommission: func(ctx context.Context, state desiredState) error {
		if state.BlockChinaInbound || state.Xray.Enabled || !state.Xray.Purge || len(state.Xray.Proxies) != 0 ||
			state.Realm.Enabled || !state.Realm.Purge || len(state.Realm.Relays) != 0 {
			return errUnsupportedManagedConfig
		}
		if err := xray.purge(ctx); err != nil {
			return err
		}
		if err := realm.purge(ctx); err != nil {
			return err
		}
		if err := hopping.apply(ctx, nil); err != nil {
			return err
		}
		if err := singbox.purge(ctx); err != nil {
			return err
		}
		if err := chinaFirewall.purge(ctx); err != nil {
			return err
		}
		if acme != nil {
			if err := acme.purge(ctx); err != nil {
				return err
			}
		}
		if err := singboxACME.purge(ctx); err != nil {
			return err
		}
		log.Print("Managed firewall cleanup complete")
		return nil
	}}
	return synchronizer
}

func applySingboxAndHopping(ctx context.Context, state desiredState, singbox *singboxManager, refresh func(context.Context, desiredState) error, components map[string]string) error {
	var previous []byte
	var hadPrevious bool
	var previousDomains []string
	if state.Hysteria2HoppingControl && state.Singbox != nil {
		var err error
		previous, hadPrevious, err = readOptionalFile(singbox.configPath)
		if err != nil {
			components["singbox"], components["hy2_port_hopping"] = "failed", "unknown"
			return fmt.Errorf("%w: read previous configuration", errSingboxConfigWrite)
		}
		previousDomains = append([]string(nil), singbox.domains...)
	}
	if state.Singbox != nil {
		if err := singbox.apply(ctx, *state.Singbox); err != nil {
			components["singbox"] = "failed"
			if state.Hysteria2HoppingControl {
				components["hy2_port_hopping"] = "unknown"
			}
			return err
		}
		components["singbox"] = "success"
	}
	if !state.Hysteria2HoppingControl {
		return nil
	}
	if err := refresh(ctx, state); err != nil {
		components["hy2_port_hopping"] = "failed"
		if errors.Is(err, errHoppingUnsupported) {
			components["hy2_port_hopping"] = "unsupported"
		}
		if state.Singbox != nil {
			current, exists, readErr := readOptionalFile(singbox.configPath)
			if readErr != nil {
				components["singbox"] = "failed"
				return errors.Join(err, errSingboxConfigWrite)
			}
			if exists != hadPrevious || !bytes.Equal(previous, current) {
				err = singbox.rollback(ctx, previous, hadPrevious, err)
				singbox.domains = previousDomains
				components["singbox"] = "failed"
			}
		}
		return err
	}
	components["hy2_port_hopping"] = "success"
	return nil
}

func applyManagedComponents(ctx context.Context, state desiredState, applyXray func(context.Context, desiredState) error, applyRealm func(context.Context, desiredRealmState) error, firewall *chinaInboundFirewall) (map[string]string, error) {
	xrayErr := applyXray(ctx, state)
	realmErr := applyRealm(ctx, state.Realm)
	firewallErr := firewall.apply(ctx, state)
	status := func(err error) string {
		if err == nil {
			return "success"
		}
		return "failed"
	}
	chinaStatus := status(firewallErr)
	if firewall.legacyUnverified {
		chinaStatus = "unknown"
	}
	if state.BlockChinaInbound && (errors.Is(firewallErr, errChinaNFTPrivileges) || errors.Is(firewallErr, errChinaNFTKernel) || errors.Is(firewallErr, errManagedChinaInboundRequiresNFT)) {
		chinaStatus = "unsupported"
	}
	environment := probeChinaFirewall(ctx, firewall)
	return map[string]string{"xray": status(xrayErr), "realm": status(realmErr), "china_firewall": chinaStatus, "firewall_environment": environment}, errors.Join(xrayErr, realmErr, firewallErr)
}

func (s *configSynchronizer) diagnose(ctx context.Context, requestID string) diagnostic.Result {
	startedAt := s.now()
	state, err := s.fetch(ctx)
	checks := make([]diagnostic.Check, 0)
	if err != nil {
		checks = append(checks,
			diagnostic.Check{Code: "config.desired_state", Status: diagnostic.StatusFail, Detail: "无法读取当前 desired state：" + err.Error()},
			diagnostic.Check{Code: "xray.service", Status: diagnostic.StatusSkipped, Detail: "无法读取当前 desired state，未执行 Xray 检查"},
			diagnostic.Check{Code: "xray.config", Status: diagnostic.StatusSkipped, Detail: "无法读取当前 desired state，未执行 Xray 配置检查"},
			diagnostic.Check{Code: "realm.service", Status: diagnostic.StatusSkipped, Detail: "无法读取当前 desired state，未执行 Realm 检查"},
		)
	} else {
		checks = s.diagnoseState(ctx, state)
	}
	return diagnostic.Result{
		Type: "diagnostic_result", RequestID: requestID, StartedAt: startedAt.Unix(),
		DurationMS: time.Since(startedAt).Milliseconds(), Checks: checks,
	}
}

func (s *configSynchronizer) sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fetchContext, cancelFetch := context.WithTimeout(ctx, configRequestTimeout)
	state, err := s.fetch(fetchContext)
	cancelFetch()
	if err != nil {
		return err
	}
	if state.Version == s.lastSuccessfulVersion {
		if !state.Hysteria2HoppingControl || s.refreshHopping == nil {
			return nil
		}
		refreshCtx, cancelRefresh := context.WithTimeout(ctx, configRequestTimeout)
		refreshErr := s.refreshHopping(refreshCtx, state)
		cancelRefresh()
		if refreshErr == nil {
			return nil
		}
		s.lastSuccessfulVersion = 0
	}
	if state.Version == s.firewallRetryVersion && s.now().Before(s.firewallRetryAt) {
		return nil
	}

	applyContext, cancelApply := context.WithTimeout(ctx, configApplyTimeout)
	var applyErr error
	var prepared *preparedSelfUninstall
	if state.Decommission {
		applyErr = s.applyDecommission(applyContext, state)
		if applyErr == nil {
			prepared, applyErr = s.prepareSelfUninstall()
			if applyErr != nil {
				applyErr = fmt.Errorf("%w: %v", errAgentSelfUninstallLaunch, applyErr)
			}
		}
	} else {
		s.components = nil
		applyErr = s.applyState(applyContext, state)
	}
	cancelApply()
	result := configResult{Version: state.Version, Status: "success"}
	if state.ComponentResults && !state.Decommission {
		result.Components = s.components
	}
	if applyErr != nil {
		result.Status = "failed"
		result.Message = desiredStateErrorMessage(applyErr)
	} else if !state.Decommission {
		s.lastSuccessfulState = state
		s.hasSuccessfulState = true
	}
	reportContext, cancelReport := context.WithTimeout(ctx, configRequestTimeout)
	err = s.report(reportContext, result)
	cancelReport()
	if err != nil {
		if prepared != nil {
			prepared.cleanup()
		}
		return err
	}
	if applyErr != nil {
		joined, multiple := applyErr.(interface{ Unwrap() []error })
		firewallOnly := !multiple || len(joined.Unwrap()) == 1
		if firewallOnly && (errors.Is(applyErr, errManagedChinaInboundFirewall) || errors.Is(applyErr, errManagedChinaInboundRequiresNFT) || errors.Is(applyErr, errChinaNFTPrivileges) || errors.Is(applyErr, errChinaNFTKernel)) {
			if state.Version != s.firewallRetryVersion {
				s.firewallRetryDelay = 0
			}
			minimum := 30 * time.Second
			if errors.Is(applyErr, errChinaNFTPrivileges) || errors.Is(applyErr, errChinaNFTKernel) || errors.Is(applyErr, errManagedChinaInboundRequiresNFT) {
				minimum = 5 * time.Minute
			}
			s.firewallRetryDelay = min(max(minimum, s.firewallRetryDelay*2), 30*time.Minute)
			s.firewallRetryVersion = state.Version
			s.firewallRetryAt = s.now().Add(s.firewallRetryDelay)
		}
		return applyErr
	}
	s.firewallRetryVersion = 0
	s.firewallRetryAt = time.Time{}
	s.firewallRetryDelay = 0
	if prepared != nil {
		if err := prepared.activate(); err != nil {
			prepared.cleanup()
			return fmt.Errorf("%w: %v", errAgentSelfUninstallLaunch, err)
		}
		log.Print("Agent self-uninstall scheduled")
	}
	s.lastSuccessfulVersion = state.Version
	return nil
}

func (s *configSynchronizer) capabilitiesChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSuccessfulVersion = 0
	s.firewallRetryVersion = 0
	s.firewallRetryAt = time.Time{}
	s.firewallRetryDelay = 0
}

func (s *configSynchronizer) renew(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renewCertificates == nil {
		return nil
	}
	return s.renewCertificates(ctx)
}

func (s *configSynchronizer) refreshChinaPrefixes(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chinaFirewall == nil || !s.hasSuccessfulState || !s.lastSuccessfulState.BlockChinaInbound {
		return nil
	}
	return s.chinaFirewall.refresh(ctx, s.lastSuccessfulState)
}

func (s *configSynchronizer) fetch(ctx context.Context) (desiredState, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.PanelURL+"/api/agent/config", nil)
	if err != nil {
		return desiredState{}, fmt.Errorf("create Agent config request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.config.AgentToken)
	response, err := s.client.Do(request)
	if err != nil {
		return desiredState{}, fmt.Errorf("fetch Agent config: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return desiredState{}, panelConfigResponseError(response, s.config.AgentToken)
	}
	var state desiredState
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&state); err != nil {
		return desiredState{}, fmt.Errorf("decode Agent config: %w", err)
	}
	if state.Version <= 0 {
		return desiredState{}, errors.New("Panel returned an invalid Agent config version")
	}
	return state, nil
}

func panelConfigResponseError(response *http.Response, agentToken string) error {
	const maxBodyBytes = 4 << 10
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if readErr != nil {
		return fmt.Errorf("Panel rejected Agent config request: %s", response.Status)
	}
	truncated := len(body) > maxBodyBytes
	if truncated {
		body = body[:maxBodyBytes]
	}
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		if detail := safePanelErrorDetail(payload.Error, agentToken); detail != "" {
			return fmt.Errorf("Panel rejected Agent config request: %s: %s", response.Status, detail)
		}
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !truncated && len(body) <= 512 && strings.HasPrefix(contentType, "text/plain") && utf8.Valid(body) {
		detail := strings.Join(strings.Fields(string(body)), " ")
		if !strings.ContainsAny(detail, "<>") {
			if detail = safePanelErrorDetail(detail, agentToken); detail != "" {
				return fmt.Errorf("Panel rejected Agent config request: %s: %s", response.Status, detail)
			}
		}
	}
	return fmt.Errorf("Panel rejected Agent config request: %s", response.Status)
}

func safePanelErrorDetail(value, agentToken string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || (agentToken != "" && strings.Contains(value, agentToken)) {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 512 {
		value = string(runes[:512]) + "…"
	}
	return value
}

func (s *configSynchronizer) report(ctx context.Context, result configResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode Agent config result: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, s.config.PanelURL+"/api/agent/config/result", bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create Agent config result request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.config.AgentToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("report Agent config result: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Panel rejected Agent config result: %s", response.Status)
	}
	return nil
}

func desiredStateErrorMessage(err error) string {
	for _, publicError := range []error{
		errUnsupportedInitSystem,
		errUnsupportedManagedConfig,
		errManagedXrayDownload,
		errManagedXrayChecksum,
		errManagedXrayValidation,
		errManagedXrayStart,
		errManagedXrayHealth,
		errManagedXrayStop,
		errManagedXrayConflict,
		errManagedXrayArch,
		errManagedProxyFirewall,
		errManagedACME,
		errManagedRealmDownload,
		errManagedRealmChecksum,
		errManagedRealmValidation,
		errManagedRealmStart,
		errManagedRealmHealth,
		errManagedRealmStop,
		errManagedRealmConflict,
		errManagedRealmArch,
		errManagedRealmFirewall,
		errSingboxDownload,
		errSingboxChecksum,
		errSingboxValidation,
		errSingboxStart,
		errSingboxHealth,
		errSingboxTLS,
		errSingboxArchitecture,
		errSingboxConflict,
		errSingboxBinary,
		errSingboxInstall,
		errSingboxConfigWrite,
		errSingboxTLSSetup,
		errSingboxServiceSetup,
		errHoppingUnsupported,
		errHoppingConflict,
		errHoppingApply,
		errManagedChinaInboundRequiresNFT,
		errChinaNFTCleanup,
		errChinaNFTTooLarge,
		errChinaNFTInterval,
		errChinaNFTPrivileges,
		errChinaNFTKernel,
		errChinaNFTLoad,
		errChinaNFTActivation,
		errChinaNFTInspect,
		errChinaNFTApply,
		errManagedChinaInboundFirewall,
		errManagedRuntimePurge,
		errAgentSelfUninstallLaunch,
	} {
		if err.Error() == publicError.Error() || errors.Is(err, publicError) {
			return publicError.Error()
		}
	}
	return "managed runtime apply failed"
}

func attemptConfigSync(ctx context.Context, synchronizer *configSynchronizer) {
	if err := synchronizer.sync(ctx); err != nil && ctx.Err() == nil {
		log.Printf("Agent config sync failed: %v", err)
	}
}
