package api

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

type createClientRequest struct {
	Name         string          `json:"name"`
	ClientUDP443 bool            `json:"client_udp443"`
	Enabled      *bool           `json:"enabled"`
	ExpiresAt    json.RawMessage `json:"expires_at"`
	clientTrafficRequest
}

type updateClientRequest struct {
	Name         *string         `json:"name"`
	ClientUDP443 *bool           `json:"client_udp443"`
	Enabled      *bool           `json:"enabled"`
	ExpiresAt    json.RawMessage `json:"expires_at"`
	clientTrafficRequest
}

type clientTrafficRequest struct {
	TrafficLimit        json.RawMessage `json:"traffic_limit"`
	LimitUnit           *string         `json:"limit_unit"`
	TrafficResetMode    *string         `json:"traffic_reset_mode"`
	TrafficResetWeekday *int            `json:"traffic_reset_weekday"`
	TrafficResetDay     *int            `json:"traffic_reset_day"`
	TrafficResetTime    *string         `json:"traffic_reset_time"`
}

type clientSummaryResponse struct {
	ID                  int64                 `json:"id"`
	ProxyID             int64                 `json:"proxy_id"`
	Name                string                `json:"name"`
	ClientUDP443        bool                  `json:"client_udp443"`
	Enabled             bool                  `json:"enabled"`
	ExpiresAt           *time.Time            `json:"expires_at"`
	Expired             bool                  `json:"expired"`
	QuotaExhausted      bool                  `json:"quota_exhausted"`
	EffectiveEnabled    bool                  `json:"effective_enabled"`
	Status              string                `json:"status"`
	TrafficLimitBytes   *int64                `json:"traffic_limit_bytes"`
	TrafficResetMode    string                `json:"traffic_reset_mode"`
	TrafficResetWeekday int                   `json:"traffic_reset_weekday"`
	TrafficResetDay     int                   `json:"traffic_reset_day"`
	TrafficResetTime    string                `json:"traffic_reset_time"`
	NextResetAt         *time.Time            `json:"next_reset_at"`
	Metrics             clientMetricsResponse `json:"metrics"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
}

type clientResponse struct {
	ID                  int64                 `json:"id"`
	ProxyID             int64                 `json:"proxy_id"`
	Name                string                `json:"name"`
	ClientUDP443        bool                  `json:"client_udp443"`
	Enabled             bool                  `json:"enabled"`
	ExpiresAt           *time.Time            `json:"expires_at"`
	Expired             bool                  `json:"expired"`
	QuotaExhausted      bool                  `json:"quota_exhausted"`
	EffectiveEnabled    bool                  `json:"effective_enabled"`
	Status              string                `json:"status"`
	TrafficLimitBytes   *int64                `json:"traffic_limit_bytes"`
	TrafficResetMode    string                `json:"traffic_reset_mode"`
	TrafficResetWeekday int                   `json:"traffic_reset_weekday"`
	TrafficResetDay     int                   `json:"traffic_reset_day"`
	TrafficResetTime    string                `json:"traffic_reset_time"`
	NextResetAt         *time.Time            `json:"next_reset_at"`
	Metrics             clientMetricsResponse `json:"metrics"`
	CreatedAt           time.Time             `json:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at"`
}

type clientMetricsResponse struct {
	CycleUplinkBytes   int64      `json:"cycle_uplink_bytes"`
	CycleDownlinkBytes int64      `json:"cycle_downlink_bytes"`
	UsedBytes          int64      `json:"used_bytes"`
	CycleStartedAt     *time.Time `json:"cycle_started_at"`
	LastActivityAt     *time.Time `json:"last_activity_at"`
	UpdatedAt          *time.Time `json:"updated_at"`
}

type clientShareResponse struct {
	Client      clientResponse `json:"client"`
	ProxyName   string         `json:"proxy_name"`
	Address     string         `json:"address"`
	Port        int            `json:"port"`
	Protocol    string         `json:"protocol"`
	Method      string         `json:"method,omitempty"`
	Network     string         `json:"network,omitempty"`
	Security    string         `json:"security,omitempty"`
	ServerName  string         `json:"server_name,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Flow        string         `json:"flow,omitempty"`
	URI         string         `json:"uri"`
}

func toClientSummaryResponse(value proxystore.ClientSummary) clientSummaryResponse {
	client := proxystore.Client{
		Enabled: value.Enabled, ExpiresAt: value.ExpiresAt, TrafficLimitBytes: value.TrafficLimitBytes,
		TrafficResetMode: value.TrafficResetMode, TrafficResetWeekday: value.TrafficResetWeekday,
		TrafficResetDay: value.TrafficResetDay, TrafficResetTime: value.TrafficResetTime, Metrics: value.Metrics,
	}
	lifecycle := client.LifecycleAt(time.Now())
	return clientSummaryResponse{
		ID: value.ID, ProxyID: value.ProxyID, Name: value.Name,
		ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
		Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
		EffectiveEnabled: lifecycle.EffectiveEnabled, Status: lifecycle.Status,
		TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
		TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
		TrafficResetTime: value.TrafficResetTime, NextResetAt: client.NextResetAt(time.Now()),
		Metrics: toClientMetricsResponse(value.Metrics), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func toClientResponse(value proxystore.Client) clientResponse {
	lifecycle := value.LifecycleAt(time.Now())
	return clientResponse{
		ID: value.ID, ProxyID: value.ProxyID, Name: value.Name,
		ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
		Expired: lifecycle.Expired, QuotaExhausted: lifecycle.QuotaExhausted,
		EffectiveEnabled: lifecycle.EffectiveEnabled, Status: lifecycle.Status,
		TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
		TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
		TrafficResetTime: value.TrafficResetTime, NextResetAt: value.NextResetAt(time.Now()),
		Metrics: toClientMetricsResponse(value.Metrics), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func toClientMetricsResponse(value *proxystore.ClientMetrics) clientMetricsResponse {
	if value == nil {
		return clientMetricsResponse{}
	}
	cycleStartedAt, updatedAt := value.CycleStartedAt, value.UpdatedAt
	return clientMetricsResponse{
		CycleUplinkBytes: value.CycleUplinkBytes, CycleDownlinkBytes: value.CycleDownlinkBytes,
		UsedBytes:      proxystore.ClientUsedBytes(value),
		CycleStartedAt: &cycleStartedAt, LastActivityAt: value.LastActivityAt, UpdatedAt: &updatedAt,
	}
}

var clientTrafficAmountPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)

func parseClientExpiration(raw json.RawMessage) (*time.Time, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, true, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return nil, true, proxystore.ErrInvalidClientExpiration
	}
	value = strings.TrimSpace(value)
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
			parsed, err = time.ParseInLocation(layout, value, shanghaiLocation)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return nil, true, proxystore.ErrInvalidClientExpiration
	}
	result := parsed.UTC().Truncate(time.Second)
	return &result, true, nil
}

func hasClientTrafficRequest(request clientTrafficRequest) bool {
	return len(request.TrafficLimit) != 0 || request.LimitUnit != nil || request.TrafficResetMode != nil ||
		request.TrafficResetWeekday != nil || request.TrafficResetDay != nil || request.TrafficResetTime != nil
}

func parseClientTrafficRequest(
	request clientTrafficRequest,
	base proxystore.ClientTrafficConfig,
) (proxystore.ClientTrafficConfig, bool, error) {
	hasAny := hasClientTrafficRequest(request)
	if base.ResetMode == "" {
		base.ResetMode = proxystore.TrafficResetNever
		base.Weekday = 1
		base.Day = 1
		base.ResetTime = "00:00"
	}
	if len(request.TrafficLimit) != 0 {
		limit, err := parseClientTrafficLimit(request.TrafficLimit, request.LimitUnit)
		if err != nil {
			return proxystore.ClientTrafficConfig{}, hasAny, err
		}
		base.LimitBytes = limit
	} else if request.LimitUnit != nil {
		return proxystore.ClientTrafficConfig{}, hasAny, proxystore.ErrInvalidClientTrafficConfig
	}
	if request.TrafficResetMode != nil {
		base.ResetMode = strings.TrimSpace(*request.TrafficResetMode)
	}
	if request.TrafficResetWeekday != nil {
		base.Weekday = *request.TrafficResetWeekday
	}
	if request.TrafficResetDay != nil {
		base.Day = *request.TrafficResetDay
	}
	if request.TrafficResetTime != nil {
		base.ResetTime = strings.TrimSpace(*request.TrafficResetTime)
	}
	return base, hasAny, nil
}

func parseClientTrafficLimit(raw json.RawMessage, unit *string) (*int64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "null" || value == `""` || value == "0" || value == `"0"` {
		return nil, nil
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		var decoded string
		if json.Unmarshal(raw, &decoded) != nil {
			return nil, proxystore.ErrInvalidClientTrafficConfig
		}
		value = strings.TrimSpace(decoded)
	}
	if !clientTrafficAmountPattern.MatchString(value) {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || amount < 0 {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	if amount == 0 {
		return nil, nil
	}
	unitValue := "G"
	if unit != nil {
		unitValue = strings.ToUpper(strings.TrimSpace(*unit))
	}
	multiplier := float64(int64(1) << 30)
	if unitValue == "T" {
		multiplier = float64(int64(1) << 40)
	} else if unitValue != "G" {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	bytes := math.Round(amount * multiplier)
	if bytes <= 0 || bytes > float64(math.MaxInt64) {
		return nil, proxystore.ErrInvalidClientTrafficConfig
	}
	result := int64(bytes)
	return &result, nil
}
