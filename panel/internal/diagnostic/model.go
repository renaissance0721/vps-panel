package diagnostic

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	CapabilityV1          = "diagnostics_v1"
	StatusPass            = "pass"
	StatusWarning         = "warning"
	StatusFail            = "fail"
	StatusSkipped         = "skipped"
	MaxChecks             = 128
	MaxResultBytes        = 60 << 10
	MaxDetailBytes        = 512
	MaxLabelBytes         = 128
	MaxEndpointBytes      = 256
	MaxProtocolBytes      = 16
	RequestIDEncodedBytes = 32
)

var (
	ErrInvalidRequestID = errors.New("invalid diagnostic request ID")
	ErrInvalidResult    = errors.New("invalid diagnostic result")
)

type Request struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
}

type Check struct {
	Code          string `json:"code"`
	Status        string `json:"status"`
	ResourceID    *int64 `json:"resource_id,omitempty"`
	Label         string `json:"label,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	LatencyMS     *int64 `json:"latency_ms,omitempty"`
	ExpiresAt     *int64 `json:"expires_at,omitempty"`
	RemainingDays *int64 `json:"remaining_days,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type Result struct {
	Type       string  `json:"type"`
	RequestID  string  `json:"request_id"`
	StartedAt  int64   `json:"started_at"`
	DurationMS int64   `json:"duration_ms"`
	Checks     []Check `json:"checks"`
}

func NewRequestID() (string, error) {
	value := make([]byte, RequestIDEncodedBytes/2)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func ValidRequestID(value string) bool {
	if len(value) != RequestIDEncodedBytes {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == RequestIDEncodedBytes/2
}

func ValidateResult(value Result) error {
	if value.Type != "diagnostic_result" || !ValidRequestID(value.RequestID) || value.StartedAt <= 0 ||
		value.DurationMS < 0 || value.DurationMS > 60_000 || len(value.Checks) > MaxChecks {
		return ErrInvalidResult
	}
	for _, check := range value.Checks {
		if !validCheck(check) {
			return ErrInvalidResult
		}
	}
	return nil
}

func EncodeResult(value Result) ([]byte, error) {
	value.Type = "diagnostic_result"
	for index := range value.Checks {
		value.Checks[index].Label = SafeText(value.Checks[index].Label, MaxLabelBytes)
		value.Checks[index].Endpoint = SafeText(value.Checks[index].Endpoint, MaxEndpointBytes)
		value.Checks[index].Protocol = SafeText(value.Checks[index].Protocol, MaxProtocolBytes)
		value.Checks[index].Detail = SafeText(value.Checks[index].Detail, MaxDetailBytes)
	}
	if len(value.Checks) > MaxChecks {
		value.Checks = append(value.Checks[:MaxChecks-1], truncatedCheck())
	}
	for {
		if err := ValidateResult(value); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if len(payload) <= MaxResultBytes {
			return payload, nil
		}
		if len(value.Checks) <= 1 {
			return nil, ErrInvalidResult
		}
		value.Checks = append(value.Checks[:len(value.Checks)-2], truncatedCheck())
	}
}

func SafeText(value string, maxBytes int) string {
	value = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, strings.TrimSpace(value))
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return strings.TrimSpace(value[:maxBytes])
}

func validCheck(value Check) bool {
	if !allowedCodes[value.Code] || !allowedStatuses[value.Status] ||
		!validText(value.Label, MaxLabelBytes) || !validText(value.Endpoint, MaxEndpointBytes) ||
		!validText(value.Protocol, MaxProtocolBytes) || !validText(value.Detail, MaxDetailBytes) {
		return false
	}
	if value.ResourceID != nil && *value.ResourceID <= 0 {
		return false
	}
	if value.LatencyMS != nil && (*value.LatencyMS < 0 || *value.LatencyMS > 60_000) {
		return false
	}
	if value.ExpiresAt != nil && *value.ExpiresAt <= 0 {
		return false
	}
	return value.RemainingDays == nil || *value.RemainingDays >= 0
}

func validText(value string, maxBytes int) bool {
	return utf8.ValidString(value) && len(value) <= maxBytes && strings.IndexFunc(value, func(character rune) bool {
		return character < 0x20 || character == 0x7f
	}) == -1
}

func truncatedCheck() Check {
	return Check{Code: "diagnostic.truncated", Status: StatusWarning, Detail: "诊断项目过多，结果已按安全限制截断"}
}

var allowedStatuses = map[string]bool{
	StatusPass: true, StatusWarning: true, StatusFail: true, StatusSkipped: true,
}

var allowedCodes = map[string]bool{
	"agent.connected":      true,
	"config.version":       true,
	"config.sync":          true,
	"xray.service":         true,
	"xray.config":          true,
	"xray.listener":        true,
	"realm.service":        true,
	"realm.listener":       true,
	"relay.dns":            true,
	"relay.target_tcp":     true,
	"tls.certificate":      true,
	"panel.entry_tcp":      true,
	"protocol.end_to_end":  true,
	"diagnostic.truncated": true,
}
