package monitor

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxProbeTasks    = 64
	MinProbeInterval = 5
	MaxProbeInterval = 86400
	CapabilityTCP    = "probe.tcp"
	CapabilityICMP   = "probe.icmp"
)

var (
	ErrInvalid       = errors.New("invalid probe")
	ErrNotFound      = errors.New("probe task not found")
	ErrUnsupported   = errors.New("Agent 未声明支持此探测类型")
	ErrTaskLimit     = errors.New("each server supports at most 64 probe tasks")
	ErrInvalidResult = errors.New("invalid probe result")
)

type ProbeTask struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Target          string `json:"target"`
	Port            *int   `json:"port"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type Task struct {
	ProbeTask
	Enabled   bool      `json:"enabled"`
	DefaultOn bool      `json:"default_on"`
	ServerIDs []int64   `json:"server_ids"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DesiredTasks struct {
	Type    string      `json:"type"`
	Version int64       `json:"version"`
	Tasks   []ProbeTask `json:"tasks"`
}

type ProbeResult struct {
	Type      string   `json:"type"`
	TaskID    int64    `json:"task_id"`
	Outcome   string   `json:"outcome"`
	LatencyMS *float64 `json:"latency_ms,omitempty"`
}

type ProbeRecord struct {
	TaskID    int64     `json:"task_id"`
	Timestamp time.Time `json:"timestamp"`
	Outcome   string    `json:"outcome"`
	LatencyMS *float64  `json:"latency_ms"`
}

type ProbeSummary struct {
	ProbeTask
	LatestLatencyMS *float64 `json:"latest_latency_ms"`
	LatestOutcome   string   `json:"latest_outcome"`
	FailureRate     *float64 `json:"failure_rate"`
}

type History struct {
	RangeHours int            `json:"range_hours"`
	Tasks      []ProbeSummary `json:"tasks"`
	Samples    []ProbeRecord  `json:"samples"`
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
}

func Supports(capabilities map[string]bool, kind string) bool {
	return (kind == "tcp" && capabilities[CapabilityTCP]) || (kind == "icmp" && capabilities[CapabilityICMP])
}

// ParseTarget translates the management API endpoint into the existing Agent fields.
func ParseTarget(kind, target string) (string, *int, error) {
	target = strings.TrimSpace(target)
	if kind == "icmp" {
		if !validTarget(target) {
			return "", nil, fmt.Errorf("%w: ICMP 探测目标必须是 IP 地址或主机名，不能包含端口", ErrInvalid)
		}
		return target, nil, nil
	}
	if kind != "tcp" {
		return "", nil, fmt.Errorf("%w: type must be tcp or icmp", ErrInvalid)
	}
	host, rawPort, err := net.SplitHostPort(target)
	if err != nil {
		if strings.Count(target, ":") > 1 && !strings.HasPrefix(target, "[") {
			return "", nil, fmt.Errorf("%w: IPv6 TCP 目标请使用 [IPv6]:端口 格式", ErrInvalid)
		}
		return "", nil, fmt.Errorf("%w: TCP 探测目标必须包含端口，例如 example.com:443", ErrInvalid)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 || strings.Trim(rawPort, "0123456789") != "" {
		return "", nil, fmt.Errorf("%w: TCP 端口必须为 1-65535 的整数", ErrInvalid)
	}
	if !validTarget(host) {
		return "", nil, fmt.Errorf("%w: TCP 探测目标必须是有效的主机名或 IP 地址", ErrInvalid)
	}
	return host, &port, nil
}

// ValidateTask is shared with the Agent: a malformed desired list cannot bypass hard limits.
func ValidateTask(task ProbeTask) error {
	if strings.TrimSpace(task.Name) == "" || utf8.RuneCountInString(task.Name) > 100 {
		return fmt.Errorf("%w: name must be 1-100 characters", ErrInvalid)
	}
	if task.Type != "tcp" && task.Type != "icmp" {
		return fmt.Errorf("%w: type must be tcp or icmp", ErrInvalid)
	}
	if !validTarget(task.Target) {
		return fmt.Errorf("%w: target must be an IP address or hostname without a port", ErrInvalid)
	}
	if task.Type == "tcp" && (task.Port == nil || *task.Port < 1 || *task.Port > 65535) || task.Type == "icmp" && task.Port != nil {
		return fmt.Errorf("%w: TCP requires port 1-65535; ICMP requires null port", ErrInvalid)
	}
	if task.IntervalSeconds < MinProbeInterval || task.IntervalSeconds > MaxProbeInterval {
		return fmt.Errorf("%w: interval must be 5-86400 seconds", ErrInvalid)
	}
	return nil
}

func validTarget(target string) bool {
	if net.ParseIP(target) != nil {
		return true
	}
	if len(target) == 0 || len(target) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(target, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}
