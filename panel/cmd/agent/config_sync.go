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
	"sync"
	"time"

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
	Version           int64             `json:"version"`
	Decommission      bool              `json:"decommission"`
	BlockChinaInbound bool              `json:"block_china_inbound"`
	Xray              desiredXrayState  `json:"xray"`
	Realm             desiredRealmState `json:"realm"`
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
	Clients     []desiredClient     `json:"clients"`
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
	Version int64  `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type configSynchronizer struct {
	config                config
	client                *http.Client
	applyState            func(context.Context, desiredState) error
	applyDecommission     func(context.Context, desiredState) error
	prepareSelfUninstall  func() (*preparedSelfUninstall, error)
	renewCertificates     func(context.Context) error
	diagnoseState         func(context.Context, desiredState) []diagnostic.Check
	chinaFirewall         *chinaInboundFirewall
	now                   func() time.Time
	mu                    sync.Mutex
	lastSuccessfulVersion int64
	lastSuccessfulState   desiredState
	hasSuccessfulState    bool
}

func newConfigSynchronizer(value config, client *http.Client) *configSynchronizer {
	xray := newXrayManager()
	realm := newRealmManager()
	chinaFirewall := newChinaInboundFirewall()
	acme := xray.acme
	runner := newAgentDiagnosticRunner(xray, realm)
	return &configSynchronizer{config: value, client: client, renewCertificates: xray.renewCertificates, diagnoseState: runner.run, chinaFirewall: chinaFirewall, now: time.Now, prepareSelfUninstall: prepareAgentSelfUninstall, applyState: func(ctx context.Context, state desiredState) error {
		xrayErr := xray.apply(ctx, state)
		realmErr := realm.apply(ctx, state.Realm)
		chinaFirewallErr := chinaFirewall.apply(ctx, state)
		return errors.Join(xrayErr, realmErr, chinaFirewallErr)
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
		if err := chinaFirewall.purge(ctx); err != nil {
			return err
		}
		if acme != nil {
			if err := acme.purge(ctx); err != nil {
				return err
			}
		}
		log.Print("Managed firewall cleanup complete")
		return nil
	}}
}

func (s *configSynchronizer) diagnose(ctx context.Context, requestID string) diagnostic.Result {
	startedAt := s.now()
	state, err := s.fetch(ctx)
	checks := make([]diagnostic.Check, 0)
	if err != nil {
		checks = append(checks,
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
		applyErr = s.applyState(applyContext, state)
	}
	cancelApply()
	result := configResult{Version: state.Version, Status: "success"}
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
		return applyErr
	}
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
		return desiredState{}, fmt.Errorf("Panel rejected Agent config request: %s", response.Status)
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
		errManagedChinaInboundRequiresNFT,
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
