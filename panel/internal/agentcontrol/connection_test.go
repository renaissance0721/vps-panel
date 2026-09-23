package agentcontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReplacedConnectionCannotUntrackOrReportForCurrentAgent(t *testing.T) {
	service := NewService(nil, time.Now)
	previous, current := &Connection{}, &Connection{}
	service.TrackConnection(7, previous)
	if got := service.TrackConnection(7, current); got != previous {
		t.Fatal("replacement lost previous connection")
	}
	if service.UntrackConnection(7, previous) {
		t.Fatal("stale untrack removed current connection")
	}
	called := false
	accepted, err := service.WithCurrentConnection(7, previous, func(context.Context) error { called = true; return nil })
	if err != nil || accepted || called {
		t.Fatal("stale connection reported data")
	}
	// With a nil DB this also proves the stale disconnect never reaches the status update.
	if accepted, err := service.DisconnectCurrent(7, previous); err != nil || accepted {
		t.Fatal("stale disconnect changed status")
	}
	if !service.IsCurrentConnection(7, current) {
		t.Fatal("replacement connection was lost")
	}
}

func TestAgentUpgradeNotificationRejectsReconnectedNewerAgent(t *testing.T) {
	const serverID = int64(7)
	for _, test := range []struct {
		connectedVersion string
		want             error
	}{
		{"v0.19.2", ErrAgentNewer},
		{"v0.19.1", ErrAgentAlreadyCurrent},
		{"dev", ErrUnknownAgentVersion},
	} {
		t.Run(test.connectedVersion, func(t *testing.T) {
			reconnected := &Connection{Version: test.connectedVersion}
			s := &Service{connections: map[int64]*Connection{serverID: reconnected}}
			if err := s.NotifyAgentUpgrade(serverID, "v0.19.1"); !errors.Is(err, test.want) {
				t.Fatalf("notify reconnected Agent = %v, want %v", err, test.want)
			}
		})
	}

	stale := &Connection{Version: "v0.16.0"}
	s := &Service{connections: map[int64]*Connection{serverID: &Connection{Version: "v0.19.2"}}}
	if s.IsCurrentConnection(serverID, stale) {
		t.Fatal("stale Agent connection remained current after reconnect")
	}
}

func TestAgentUpgradeNotificationRequiresOfficialSelfUpgradeIdentity(t *testing.T) {
	const serverID = int64(8)
	for _, connection := range []*Connection{
		{Implementation: "io.github.matthewlu070111.boardray", APIVersion: 1, Version: "v0.1.0", Capabilities: map[string]bool{CapabilitySelfUpgrade: true}},
		{Implementation: OfficialImplementation, APIVersion: 1, Version: "v0.1.0", Capabilities: map[string]bool{}},
	} {
		service := &Service{connections: map[int64]*Connection{serverID: connection}}
		if err := service.NotifyAgentUpgrade(serverID, "v0.2.0"); !errors.Is(err, ErrAgentUpgradeUnsupported) {
			t.Fatalf("NotifyAgentUpgrade(%+v) = %v", connection, err)
		}
	}
	official := &Connection{
		Implementation: OfficialImplementation,
		APIVersion:     CurrentAPIVersion,
		Version:        "v0.2.0",
		Capabilities:   map[string]bool{CapabilitySelfUpgrade: true},
	}
	service := &Service{connections: map[int64]*Connection{serverID: official}}
	if err := service.NotifyAgentUpgrade(serverID, "v0.2.0"); !errors.Is(err, ErrAgentAlreadyCurrent) {
		t.Fatalf("official self-upgrade gate error = %v", err)
	}
}
