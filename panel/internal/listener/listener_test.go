package listener_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/listener"
)

func TestListenerReservationNetworkOverlap(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO servers
		(id, name, created_by_role, status, created_at, updated_at)
		VALUES (1, 'Server', 'admin', 'online', 1, 1)`); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		first    listener.Reservation
		second   listener.Reservation
		conflict bool
	}{
		{
			name:     "proxy tcp and relay tcp",
			first:    listener.Reservation{ServerID: 1, ResourceType: listener.ResourceProxy, ResourceID: 1, Port: 443, Network: listener.NetworkTCP},
			second:   listener.Reservation{ServerID: 1, ResourceType: listener.ResourceRelay, ResourceID: 2, Port: 443, Network: listener.NetworkTCP},
			conflict: true,
		},
		{
			name:     "shadowsocks both and relay tcp",
			first:    listener.Reservation{ServerID: 1, ResourceType: listener.ResourceProxy, ResourceID: 3, Port: 8388, Network: listener.NetworkBoth},
			second:   listener.Reservation{ServerID: 1, ResourceType: listener.ResourceRelay, ResourceID: 4, Port: 8388, Network: listener.NetworkTCP},
			conflict: true,
		},
		{
			name:     "relay udp and relay tcp",
			first:    listener.Reservation{ServerID: 1, ResourceType: listener.ResourceRelay, ResourceID: 5, Port: 23000, Network: listener.NetworkUDP},
			second:   listener.Reservation{ServerID: 1, ResourceType: listener.ResourceRelay, ResourceID: 6, Port: 23000, Network: listener.NetworkTCP},
			conflict: false,
		},
		{
			name:     "client relay and subscription realm",
			first:    listener.Reservation{ServerID: 1, ResourceType: listener.ResourceClientRelay, ResourceID: 100, Port: 24000, Network: listener.NetworkTCP},
			second:   listener.Reservation{ServerID: 1, ResourceType: listener.ResourceRelay, ResourceID: 7, Port: 24000, Network: listener.NetworkTCP},
			conflict: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := listener.ReserveListener(t.Context(), db, test.first, 1); err != nil {
				t.Fatal(err)
			}
			err := listener.ReserveListener(t.Context(), db, test.second, 1)
			if test.conflict != errors.Is(err, listener.ErrConflict) {
				t.Fatalf("second reservation error = %v, conflict = %t", err, test.conflict)
			}
			if err := listener.ReleaseListener(t.Context(), db, test.first.ResourceType, test.first.ResourceID); err != nil {
				t.Fatal(err)
			}
			if !test.conflict {
				if err := listener.ReleaseListener(t.Context(), db, test.second.ResourceType, test.second.ResourceID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestConcurrentListenerReservationAllowsOnlyOneWinner(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO servers
		(id, name, created_by_role, status, created_at, updated_at)
		VALUES (1, 'Server', 'admin', 'online', 1, 1)`); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for id := int64(1); id <= 2; id++ {
		go func(resourceID int64) {
			ready.Done()
			<-start
			results <- listener.ReserveListener(t.Context(), db, listener.Reservation{
				ServerID: 1, ResourceType: listener.ResourceRelay, ResourceID: resourceID,
				Port: 25000, Network: listener.NetworkTCP,
			}, 1)
		}(id)
	}
	ready.Wait()
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, listener.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected reservation error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %d success, %d conflict", successes, conflicts)
	}
}
