package server

import (
	"database/sql"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

type agentService = agentcontrol.Service
type testService struct {
	*Service
	*agentService
}

func newTestService(t *testing.T) (*testService, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	resources := NewService(db)
	return &testService{resources, agentcontrol.NewService(db, func() time.Time { return resources.now() })}, db
}
