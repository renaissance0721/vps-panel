package agentcontrol

import (
	"database/sql"
	"sync"
	"time"
)

// Service owns Agent credentials, status, configuration delivery and upgrades.
type Service struct {
	db            *sql.DB
	now           func() time.Time
	connectionsMu sync.Mutex
	connections   map[int64]*Connection
}

func NewService(db *sql.DB, now func() time.Time) *Service {
	return &Service{db: db, now: now, connections: make(map[int64]*Connection)}
}
