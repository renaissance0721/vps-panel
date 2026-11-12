package subscription

import (
	"database/sql"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/landing"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
	"github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type Service struct {
	db            *sql.DB
	relays        *relay.Service
	proxies       *proxy.Service
	landings      *landing.Service
	now           func() time.Time
	remoteSecrets *mailservice.SecretStore
}

func NewService(db *sql.DB, relays *relay.Service, keyPath ...string) *Service {
	s := &Service{db: db, relays: relays, proxies: proxy.NewService(db), landings: landing.NewService(db), now: time.Now}
	if len(keyPath) > 0 {
		s.remoteSecrets = mailservice.NewRemoteSubscriptionSecretStore(keyPath[0])
	}
	return s
}
