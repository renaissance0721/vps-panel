package auth

import (
	"database/sql"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

const testPassword = "strong-password"

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db), db
}
