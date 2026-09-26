package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	loginFailureWindow    = time.Minute
	loginPairFailureLimit = 5
	loginIPFailureLimit   = 20
	maxLoginPairEntries   = 4096
	maxLoginIPEntries     = 1024
)

type loginFailure struct {
	count     int
	startedAt time.Time
}

type loginAttemptKey struct {
	ip       string
	username string
}

type loginLimiter struct {
	mu           sync.Mutex
	pairFailures map[loginAttemptKey]loginFailure
	ipFailures   map[string]loginFailure
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		pairFailures: make(map[loginAttemptKey]loginFailure),
		ipFailures:   make(map[string]loginFailure),
	}
}

func (l *loginLimiter) Allow(ip, username string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpired(now)

	pairKey := loginAttemptKey{ip: ip, username: normalizeLoginUsername(username)}
	pair, hasPair := l.pairFailures[pairKey]
	ipFailure, hasIP := l.ipFailures[ip]
	var retryAfter time.Duration
	if !hasPair && len(l.pairFailures) >= maxLoginPairEntries {
		retryAfter = loginFailureWindow
	}
	if pair.count >= loginPairFailureLimit {
		retryAfter = remainingLoginWindow(pair, now)
	}
	if !hasIP && len(l.ipFailures) >= maxLoginIPEntries {
		retryAfter = loginFailureWindow
	}
	if ipFailure.count >= loginIPFailureLimit {
		retryAfter = max(retryAfter, remainingLoginWindow(ipFailure, now))
	}
	return retryAfter <= 0, retryAfter
}

func (l *loginLimiter) RecordFailure(ip, username string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpired(now)

	key := loginAttemptKey{ip: ip, username: normalizeLoginUsername(username)}
	if _, exists := l.pairFailures[key]; exists || len(l.pairFailures) < maxLoginPairEntries {
		l.pairFailures[key] = nextLoginFailure(l.pairFailures[key], now)
	}
	if _, exists := l.ipFailures[ip]; exists || len(l.ipFailures) < maxLoginIPEntries {
		l.ipFailures[ip] = nextLoginFailure(l.ipFailures[ip], now)
	}
}

func (l *loginLimiter) Reset(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pairFailures, loginAttemptKey{ip: ip, username: normalizeLoginUsername(username)})
}

func (l *loginLimiter) pruneExpired(now time.Time) {
	for key, failure := range l.pairFailures {
		if remainingLoginWindow(failure, now) <= 0 {
			delete(l.pairFailures, key)
		}
	}
	for ip, failure := range l.ipFailures {
		if remainingLoginWindow(failure, now) <= 0 {
			delete(l.ipFailures, ip)
		}
	}
}

func nextLoginFailure(current loginFailure, now time.Time) loginFailure {
	if current.count == 0 || remainingLoginWindow(current, now) <= 0 {
		return loginFailure{count: 1, startedAt: now}
	}
	current.count++
	return current
}

func remainingLoginWindow(failure loginFailure, now time.Time) time.Duration {
	if failure.count == 0 {
		return 0
	}
	return loginFailureWindow - now.Sub(failure.startedAt)
}

func normalizeLoginUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func clientIP(r *http.Request) string {
	peer := requestPeerIP(r.RemoteAddr)
	if !peer.IsValid() {
		return r.RemoteAddr
	}
	if peer.IsLoopback() {
		for _, value := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
			if forwarded, err := netip.ParseAddr(strings.TrimSpace(value)); err == nil {
				return forwarded.Unmap().String()
			}
		}
		if forwarded, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return forwarded.Unmap().String()
		}
	}
	return peer.Unmap().String()
}

func requestPeerIP(remoteAddress string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = strings.Trim(remoteAddress, "[]")
	}
	address, _ := netip.ParseAddr(host)
	return address
}
