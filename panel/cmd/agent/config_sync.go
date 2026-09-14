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
)

const unsupportedManagedConfigMessage = "managed proxy configuration is not supported by this Agent version"

var errUnsupportedManagedConfig = errors.New(unsupportedManagedConfigMessage)

const (
	configRequestTimeout = 10 * time.Second
	configApplyTimeout   = 90 * time.Second
)

type desiredState struct {
	Version int64             `json:"version"`
	Xray    desiredXrayState  `json:"xray"`
	Realm   desiredRealmState `json:"realm"`
}

type desiredXrayState struct {
	Enabled bool           `json:"enabled"`
	Proxies []desiredProxy `json:"proxies"`
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
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
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
	mu                    sync.Mutex
	lastSuccessfulVersion int64
}

func newConfigSynchronizer(value config, client *http.Client) *configSynchronizer {
	xray := newXrayManager()
	realm := newRealmManager()
	return &configSynchronizer{config: value, client: client, applyState: func(ctx context.Context, state desiredState) error {
		xrayErr := xray.apply(ctx, state)
		realmErr := realm.apply(ctx, state.Realm)
		return errors.Join(xrayErr, realmErr)
	}}
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
	applyErr := s.applyState(applyContext, state)
	cancelApply()
	result := configResult{Version: state.Version, Status: "success"}
	if applyErr != nil {
		result.Status = "failed"
		result.Message = desiredStateErrorMessage(applyErr)
	}
	reportContext, cancelReport := context.WithTimeout(ctx, configRequestTimeout)
	err = s.report(reportContext, result)
	cancelReport()
	if err != nil {
		return err
	}
	if applyErr != nil {
		return applyErr
	}
	s.lastSuccessfulVersion = state.Version
	return nil
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
		errManagedRealmDownload,
		errManagedRealmChecksum,
		errManagedRealmValidation,
		errManagedRealmStart,
		errManagedRealmHealth,
		errManagedRealmStop,
		errManagedRealmConflict,
		errManagedRealmArch,
		errManagedRealmFirewall,
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
