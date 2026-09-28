package subscription

import (
	"database/sql"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type Service struct {
	db      *sql.DB
	relays  *relay.Service
	proxies *proxy.Service
	now     func() time.Time
}

func NewService(db *sql.DB, relays *relay.Service) *Service {
	return &Service{db: db, relays: relays, proxies: proxy.NewService(db), now: time.Now}
}
