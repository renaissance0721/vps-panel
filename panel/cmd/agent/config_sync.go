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

type desiredState struct {
	Version int64             `json:"version"`
	Xray    desiredXrayState  `json:"xray"`
	Realm   desiredRealmState `json:"realm"`
}

type desiredXrayState struct {
	Enabled bool              `json:"enabled"`
	Proxies []json.RawMessage `json:"proxies"`
}

type desiredRealmState struct {
	Enabled bool              `json:"enabled"`
	Relays  []json.RawMessage `json:"relays"`
}

type configResult struct {
	Version int64  `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type configSynchronizer struct {
	config                config
	client                *http.Client
	mu                    sync.Mutex
	lastSuccessfulVersion int64
}

func newConfigSynchronizer(value config, client *http.Client) *configSynchronizer {
	return &configSynchronizer{config: value, client: client}
}

func (s *configSynchronizer) sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.fetch(ctx)
	if err != nil {
		return err
	}
	if state.Version == s.lastSuccessfulVersion {
		return nil
	}

	applyErr := applyDesiredState(state)
	result := configResult{Version: state.Version, Status: "success"}
	if applyErr != nil {
		result.Status = "failed"
		result.Message = unsupportedManagedConfigMessage
	}
	if err := s.report(ctx, result); err != nil {
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

func applyDesiredState(state desiredState) error {
	if state.Xray.Enabled || len(state.Xray.Proxies) != 0 ||
		state.Realm.Enabled || len(state.Realm.Relays) != 0 {
		return errors.New(unsupportedManagedConfigMessage)
	}
	return nil
}

func attemptConfigSync(ctx context.Context, synchronizer *configSynchronizer) {
	syncContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := synchronizer.sync(syncContext); err != nil && ctx.Err() == nil {
		log.Printf("Agent config sync failed: %v", err)
	}
}
