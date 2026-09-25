package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func (s *server) canAccessServer(ctx context.Context, user auth.User, serverID int64) (bool, error) {
	return s.servers.CanAccess(ctx, user.ID, serverID)
}

func (s *server) requireServerAccess(w http.ResponseWriter, r *http.Request, user auth.User, serverID int64) bool {
	allowed, err := s.canAccessServer(r.Context(), user, serverID)
	if err != nil {
		writeInternalError(w)
		return false
	}
	if !allowed {
		writeServerError(w, serverstore.ErrNotFound)
		return false
	}
	return true
}

func (s *server) requireMutableServer(w http.ResponseWriter, r *http.Request, serverID int64) bool {
	if err := s.servers.EnsureMutable(r.Context(), serverID); err != nil {
		writeServerError(w, err)
		return false
	}
	return true
}

func (s *server) proxyForUser(ctx context.Context, user auth.User, id int64) (proxystore.Proxy, error) {
	value, err := s.proxies.Get(ctx, id)
	if err != nil {
		return proxystore.Proxy{}, err
	}
	allowed, err := s.canAccessServer(ctx, user, value.ServerID)
	if err != nil {
		return proxystore.Proxy{}, err
	}
	if !allowed {
		return proxystore.Proxy{}, proxystore.ErrNotFound
	}
	return value, nil
}

func (s *server) clientForUser(ctx context.Context, user auth.User, id int64) (proxystore.Client, error) {
	value, err := s.proxies.GetClient(ctx, id)
	if err != nil {
		return proxystore.Client{}, err
	}
	_, err = s.proxyForUser(ctx, user, value.ProxyID)
	if errors.Is(err, proxystore.ErrNotFound) {
		return proxystore.Client{}, proxystore.ErrClientNotFound
	}
	if err != nil {
		return proxystore.Client{}, err
	}
	return value, nil
}

func (s *server) landingForUser(ctx context.Context, user auth.User, id int64) (landingstore.Landing, error) {
	return s.landings.Get(ctx, id, user.ID)
}

func (s *server) relayForUser(ctx context.Context, user auth.User, id int64) (relaystore.Relay, error) {
	value, err := s.relays.Get(ctx, id)
	if err != nil {
		return relaystore.Relay{}, err
	}
	allowed, err := s.canAccessServer(ctx, user, value.ServerID)
	if err != nil {
		return relaystore.Relay{}, err
	}
	if !allowed {
		return relaystore.Relay{}, relaystore.ErrNotFound
	}
	if value.TargetType == relaystore.TargetProxy {
		if value.TargetProxyID == nil {
			return relaystore.Relay{}, relaystore.ErrNotFound
		}
		if _, err := s.proxyForUser(ctx, user, *value.TargetProxyID); err != nil {
			if errors.Is(err, proxystore.ErrNotFound) {
				return relaystore.Relay{}, relaystore.ErrNotFound
			}
			return relaystore.Relay{}, err
		}
	}
	if value.TargetType == relaystore.TargetLanding {
		if value.TargetLandingID == nil {
			return relaystore.Relay{}, relaystore.ErrNotFound
		}
		if _, err := s.landingForUser(ctx, user, *value.TargetLandingID); err != nil {
			if errors.Is(err, landingstore.ErrNotFound) {
				return relaystore.Relay{}, relaystore.ErrNotFound
			}
			return relaystore.Relay{}, err
		}
	}
	return value, nil
}
