package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	countryISEndpoint    = "https://api.country.is"
	geoIPLookupTimeout   = 2 * time.Second
	geoIPRetryCooldown   = 5 * time.Minute
	geoIPMaxResponseSize = 4 << 10
)

var errInvalidCountryCode = errors.New("invalid GeoIP country code")

type GeoIPProvider interface {
	CountryCode(context.Context, string) (string, error)
}

type countryISProvider struct {
	baseURL string
	client  *http.Client
}

func NewCountryISProvider() GeoIPProvider {
	return &countryISProvider{
		baseURL: countryISEndpoint,
		client: &http.Client{
			Timeout:       geoIPLookupTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (p *countryISProvider) CountryCode(ctx context.Context, ip string) (string, error) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", fmt.Errorf("look up country: invalid IP address")
	}
	endpoint, err := url.JoinPath(p.baseURL, parsed.String())
	if err != nil {
		return "", fmt.Errorf("build GeoIP request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create GeoIP request: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request GeoIP country: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request GeoIP country: unexpected HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, geoIPMaxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read GeoIP response: %w", err)
	}
	if len(body) > geoIPMaxResponseSize {
		return "", errors.New("read GeoIP response: body too large")
	}
	var result struct {
		Country string `json:"country"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode GeoIP response: %w", err)
	}
	return normalizeCountryCode(result.Country)
}

func normalizeCountryCode(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 2 {
		return "", errInvalidCountryCode
	}
	for _, letter := range []byte(value) {
		if (letter < 'A' || letter > 'Z') && (letter < 'a' || letter > 'z') {
			return "", errInvalidCountryCode
		}
	}
	return strings.ToUpper(value), nil
}

func (s *Service) lookupCountry(agentID, serverID int64, ip string, lookupAt int64) {
	ctx, cancel := context.WithTimeout(context.Background(), geoIPLookupTimeout)
	defer cancel()
	code, err := s.geoIPProvider.CountryCode(ctx, ip)
	if err != nil {
		return
	}
	code, err = normalizeCountryCode(code)
	if err != nil {
		return
	}
	// The IP and attempt must still match the accepted report. In particular,
	// an old A lookup must not win after A -> B -> A or an Agent rebind.
	_, _ = s.db.ExecContext(ctx,
		`UPDATE server_system_info SET country_code = ?
		 WHERE server_id = ? AND country_ip = ? AND country_lookup_at = ? AND country_code = ''
		 AND EXISTS (SELECT 1 FROM agents WHERE id = ? AND server_id = ?)`,
		code, serverID, ip, lookupAt, agentID, serverID,
	)
}
