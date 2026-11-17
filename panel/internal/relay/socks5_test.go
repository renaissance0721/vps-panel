package relay

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestSOCKS5RelayTargetRejected(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertRelayTestServer(t, db, 1, "source", "198.51.100.10")
	for _, sql := range []string{`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1,'owner','hash','vip',1,1)`, `INSERT INTO landing_nodes (id,owner_user_id,name,protocol,host,port,uri,created_at,updated_at) VALUES (1,1,'node','socks5','203.0.113.10',9492,'socks5://user:pass@203.0.113.10:9492',1,1)`} {
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	id := int64(1)
	_, _, err = NewService(db).Create(t.Context(), CreateInput{ServerID: 1, Name: "relay", ListenPort: 9502, TargetType: TargetLanding, TargetLandingID: &id, Network: NetworkTCP, Enabled: true})
	if !errors.Is(err, ErrSOCKS5TargetUnsupported) {
		t.Fatalf("relay rejection: %v", err)
	}
}
