package proxy

import (
	"database/sql"
	"strings"
	"time"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

type rowScanner interface{ Scan(...any) error }

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
