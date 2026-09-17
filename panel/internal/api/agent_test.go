package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func waitForServerStatus(t *testing.T, service *serverstore.Service, serverID int64, expected string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server status: %v", err)
		}
		if value.Status == expected {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server status = %q, want %q", value.Status, expected)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForLastSeen(t *testing.T, service *serverstore.Service, serverID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server last seen: %v", err)
		}
		if value.LastSeenAt != nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("server last_seen_at was not updated")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForSystemInfo(t *testing.T, service *serverstore.Service, serverID int64, hostname string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server system information: %v", err)
		}
		if value.SystemInfo != nil && value.SystemInfo.Hostname == hostname {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server system information hostname did not become %q", hostname)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForMetrics(t *testing.T, service *serverstore.Service, serverID int64, cpuPercent float64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server metrics: %v", err)
		}
		if value.Metrics != nil && value.Metrics.CPUPercent == cpuPercent {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server CPU metrics did not become %v", cpuPercent)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func performAgentRequest(
	t *testing.T,
	handler http.Handler,
	method, target string,
	body any,
	agentToken string,
) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode Agent request: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, requestBody)
	if agentToken != "" {
		request.Header.Set("Authorization", "Bearer "+agentToken)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
