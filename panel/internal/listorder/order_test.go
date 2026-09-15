package listorder

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func orderTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	directory := t.TempDir()
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (1,'admin','hash','admin',1,1),(2,'vip','hash','vip',1,1)`,
		`INSERT INTO servers (id,name,status,visibility,desired_state_version,archived_at,created_at,updated_at) VALUES
		 (1,'A','pending','public',10,NULL,1,1),
		 (2,'B','pending','private',10,NULL,2,2),
		 (3,'C','pending','public',10,NULL,3,3),
		 (4,'D','pending','private',10,NULL,4,4),
		 (5,'E','pending','public',10,5,5,5)`,
		`INSERT INTO server_access (server_id,user_id) VALUES (2,1),(4,2)`,
		`INSERT INTO proxies (id,server_id,name,protocol,listen_port,enabled,config_json,created_at,updated_at) VALUES
		 (1,1,'PA','vless',8101,1,'{}',1,1),(2,2,'PB','vless',8102,0,'{}',2,2),
		 (3,3,'PC','vless',8103,1,'{}',3,3),(4,4,'PD','vless',8104,1,'{}',4,4)`,
		`INSERT INTO relays (id,server_id,name,listen_port,target_type,target_proxy_id,target_host,target_port,network,created_at,updated_at) VALUES
		 (1,1,'RA',9101,'manual',NULL,'example.com',443,'tcp',1,1),
		 (2,1,'RB',9102,'proxy',2,'',NULL,'tcp',2,2),
		 (3,3,'RC',9103,'manual',NULL,'example.com',443,'tcp',3,3)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	return db, directory
}

func visibleOrder(t *testing.T, db *sql.DB, userID int64, kind Kind, archived bool) []int64 {
	t.Helper()
	query := visibleQuery(kind, archived)
	args := []any{userID}
	if kind == Relays {
		args = append(args, userID)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	ordered, err := NewStore(db).Sort(context.Background(), userID, kind, ids)
	if err != nil {
		t.Fatal(err)
	}
	return ordered
}

func wantOrder(t *testing.T, db *sql.DB, userID int64, kind Kind, archived bool, want ...int64) {
	t.Helper()
	if got := visibleOrder(t, db, userID, kind, archived); !reflect.DeepEqual(got, want) {
		t.Fatalf("user %d %s archived %v = %v, want %v", userID, kind, archived, got, want)
	}
}

func TestServerOrderIsPrivatePersistentAndKeepsDefaultAndArchive(t *testing.T) {
	db, directory := orderTestDB(t)
	store := NewStore(db)
	wantOrder(t, db, 1, Servers, false, 3, 2, 1)
	wantOrder(t, db, 2, Servers, false, 4, 3, 1)
	if err := store.Move(t.Context(), 1, Servers, false, 2, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, false, 2, 3, 1)
	wantOrder(t, db, 2, Servers, false, 4, 3, 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store = NewStore(db)
	wantOrder(t, db, 1, Servers, false, 2, 3, 1)
	if _, err := db.Exec(`INSERT INTO servers (id,name,status,visibility,created_at,updated_at)
		VALUES (6,'F','pending','public',6,6),(7,'G','pending','public',7,7)`); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, false, 7, 6, 2, 3, 1)
	if _, err := db.Exec(`UPDATE servers SET archived_at = 7 WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, true, 7, 5)
	if err := store.Move(t.Context(), 1, Servers, true, 5, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, true, 5, 7)
	wantOrder(t, db, 1, Servers, false, 6, 2, 3, 1)
	if _, err := db.Exec(`DELETE FROM server_access WHERE server_id = 2 AND user_id = 1`); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, false, 6, 3, 1)
	if _, err := db.Exec(`INSERT INTO server_access (server_id,user_id) VALUES (2,1)`); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, false, 6, 2, 3, 1)
	if _, err := db.Exec(`DELETE FROM servers WHERE id = 6`); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_server_order WHERE server_id = 6`).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("deleted resource order rows = %d, %v", stale, err)
	}
}

func TestProxyAndRelayOrderExcludeHiddenResourcesAndDoNotChangeDesiredState(t *testing.T) {
	db, _ := orderTestDB(t)
	defer db.Close()
	store := NewStore(db)
	wantOrder(t, db, 1, Proxies, false, 3, 2, 1)
	wantOrder(t, db, 2, Proxies, false, 4, 3, 1)
	if err := store.Move(t.Context(), 1, Proxies, false, 2, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Proxies, false, 2, 3, 1)
	wantOrder(t, db, 2, Proxies, false, 4, 3, 1)
	if err := store.Move(t.Context(), 2, Proxies, false, 2, "up"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden Proxy reorder = %v", err)
	}
	wantOrder(t, db, 2, Relays, false, 3, 1)
	if err := store.Move(t.Context(), 2, Relays, false, 1, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 2, Relays, false, 1, 3)
	wantOrder(t, db, 1, Relays, false, 3, 2, 1)
	if err := store.Move(t.Context(), 2, Relays, false, 2, "up"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden Relay target reorder = %v", err)
	}
	if err := store.Move(t.Context(), 1, Relays, false, 1, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Relays, false, 3, 1, 2)
	if _, err := db.Exec(`INSERT INTO proxies (id,server_id,name,protocol,listen_port,enabled,config_json,created_at,updated_at)
		VALUES (6,1,'New Proxy','vless',8106,1,'{}',6,6)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays (id,server_id,name,listen_port,target_type,target_host,target_port,network,created_at,updated_at)
		VALUES (6,1,'New Relay',9106,'manual','example.com',443,'tcp',6,6)`); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Proxies, false, 6, 2, 3, 1)
	wantOrder(t, db, 1, Relays, false, 6, 3, 1, 2)
	var wrong int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE desired_state_version != 10 OR updated_at != created_at`).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatalf("reorder changed Server business state = %d, %v", wrong, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE updated_at != created_at`).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatalf("reorder changed Proxy time = %d, %v", wrong, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM relays WHERE updated_at != created_at`).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatalf("reorder changed Relay time = %d, %v", wrong, err)
	}
}

func TestReorderBoundariesRollbackAndSequentialRequests(t *testing.T) {
	db, _ := orderTestDB(t)
	defer db.Close()
	store := NewStore(db)
	for _, test := range []struct {
		id        int64
		direction string
	}{
		{3, "up"}, {1, "down"}, {5, "up"},
	} {
		if err := store.Move(t.Context(), 1, Servers, test.id == 5, test.id, test.direction); err != nil {
			t.Fatal(err)
		}
	}
	wantOrder(t, db, 1, Servers, false, 3, 2, 1)
	if err := store.Move(t.Context(), 1, Servers, false, 2, "sideways"); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("invalid direction = %v", err)
	}
	if err := store.Move(t.Context(), 1, Servers, false, 999, "up"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Server = %v", err)
	}
	if err := store.Move(t.Context(), 2, Servers, false, 2, "up"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private Server = %v", err)
	}
	if err := store.Move(t.Context(), 1, Servers, false, 2, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, false, 2, 3, 1)
	if _, err := db.Exec(`CREATE TRIGGER fail_order BEFORE INSERT ON user_server_order
		WHEN NEW.server_id = 3 BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.Move(t.Context(), 1, Servers, false, 1, "up"); err == nil {
		t.Fatal("failed transaction was committed")
	}
	wantOrder(t, db, 1, Servers, false, 2, 3, 1)
	if _, err := db.Exec(`DROP TRIGGER fail_order`); err != nil {
		t.Fatal(err)
	}
	if err := store.Move(t.Context(), 1, Servers, false, 1, "up"); err != nil {
		t.Fatal(err)
	}
	if err := store.Move(t.Context(), 1, Servers, false, 1, "up"); err != nil {
		t.Fatal(err)
	}
	wantOrder(t, db, 1, Servers, false, 1, 2, 3)
}
