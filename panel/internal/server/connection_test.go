package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestAuthenticateAgentAndUpdateConnectionStatus(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "WebSocket Agent")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}

	if _, err := service.AuthenticateAgent(context.Background(), "invalid-token"); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("AuthenticateAgent() invalid token error = %v, want ErrInvalidAgentToken", err)
	}
	agent, err := service.AuthenticateAgent(context.Background(), registered.Token)
	if err != nil {
		t.Fatalf("AuthenticateAgent() error = %v", err)
	}
	if agent.ID != registered.ID || agent.ServerID != registered.ServerID {
		t.Fatalf("AuthenticateAgent() = %+v, want agent %d for server %d", agent, registered.ID, registered.ServerID)
	}

	if err := service.SetAgentOnline(context.Background(), agent.ServerID); err != nil {
		t.Fatalf("SetAgentOnline() error = %v", err)
	}
	connected, err := service.Get(context.Background(), agent.ServerID)
	if err != nil || connected.Status != StatusOnline {
		t.Fatalf("connected server = (%+v, %v), want online", connected, err)
	}

	if err := service.SetAgentOffline(context.Background(), agent.ServerID); err != nil {
		t.Fatalf("SetAgentOffline() error = %v", err)
	}
	disconnected, err := service.Get(context.Background(), agent.ServerID)
	if err != nil || disconnected.Status != StatusOffline {
		t.Fatalf("disconnected server = (%+v, %v), want offline", disconnected, err)
	}
}

func TestAgentConnectionAndHeartbeatUpdateLastSeen(t *testing.T) {
	service, _ := newTestService(t)
	connectedAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return connectedAt }
	created, err := service.Create(context.Background(), "Heartbeat Agent")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.6.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if err := service.SetAgentConnected(context.Background(), registered.ID, registered.ServerID); err != nil {
		t.Fatalf("SetAgentConnected() error = %v", err)
	}
	connected, err := service.Get(context.Background(), registered.ServerID)
	if err != nil || connected.Status != StatusOnline || connected.LastSeenAt == nil || !connected.LastSeenAt.Equal(connectedAt) {
		t.Fatalf("connected server = (%+v, %v), want online at %v", connected, err, connectedAt)
	}

	heartbeatAt := connectedAt.Add(10 * time.Second)
	service.now = func() time.Time { return heartbeatAt }
	if err := service.TouchAgent(context.Background(), registered.ID, registered.ServerID); err != nil {
		t.Fatalf("TouchAgent() error = %v", err)
	}
	if err := service.SetAgentOffline(context.Background(), registered.ServerID); err != nil {
		t.Fatalf("SetAgentOffline() error = %v", err)
	}
	disconnected, err := service.Get(context.Background(), registered.ServerID)
	if err != nil || disconnected.Status != StatusOffline || disconnected.LastSeenAt == nil || !disconnected.LastSeenAt.Equal(heartbeatAt) {
		t.Fatalf("disconnected server = (%+v, %v), want offline with preserved last seen %v", disconnected, err, heartbeatAt)
	}
	if err := service.TouchAgent(context.Background(), registered.ID+1, registered.ServerID); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("TouchAgent() invalid identity error = %v, want ErrInvalidAgentToken", err)
	}
}

func TestResetOnlineServers(t *testing.T) {
	service, _ := newTestService(t)
	online, err := service.Create(context.Background(), "Online Agent")
	if err != nil {
		t.Fatalf("Create() online server error = %v", err)
	}
	pending, err := service.Create(context.Background(), "Pending Agent")
	if err != nil {
		t.Fatalf("Create() pending server error = %v", err)
	}
	if err := service.SetAgentOnline(context.Background(), online.ID); err != nil {
		t.Fatalf("SetAgentOnline() error = %v", err)
	}

	if err := service.ResetOnline(context.Background()); err != nil {
		t.Fatalf("ResetOnline() error = %v", err)
	}
	reset, err := service.Get(context.Background(), online.ID)
	if err != nil || reset.Status != StatusOffline {
		t.Fatalf("reset server = (%+v, %v), want offline", reset, err)
	}
	unchanged, err := service.Get(context.Background(), pending.ID)
	if err != nil || unchanged.Status != StatusPending {
		t.Fatalf("pending server = (%+v, %v), want pending", unchanged, err)
	}
}

func TestDisconnectDoesNotOverwritePendingServer(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Pending Disconnect")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.SetAgentOffline(context.Background(), created.ID); err != nil {
		t.Fatalf("SetAgentOffline() pending error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.Status != StatusPending {
		t.Fatalf("pending server after disconnect = (%+v, %v), want pending", value, err)
	}
}
