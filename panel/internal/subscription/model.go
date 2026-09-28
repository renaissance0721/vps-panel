package subscription

import (
	"errors"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

const (
	NodeModeDirect               = "direct"
	NodeModeRelay                = "relay"
	ResetModeNever               = "never"
	ResetModeMonthly             = "monthly"
	maxNameRunes                 = 100
	SubscriberStatusNormal       = "normal"
	SubscriberStatusUnconfigured = "unconfigured"
	SubscriberStatusDisabled     = "disabled"
	SubscriberStatusPlanDisabled = "plan_disabled"
	SubscriberStatusExpired      = "expired"
	SubscriberStatusExhausted    = "exhausted"
)

var (
	ErrPublishedNodeNotFound    = errors.New("published node not found")
	ErrInvalidNodeName          = errors.New("published node name must be 1-100 characters")
	ErrInvalidNodeMode          = errors.New("published node mode must be direct or relay")
	ErrTargetProxyNotFound      = errors.New("target proxy not found")
	ErrSourceProxyNotFound      = errors.New("source proxy not found")
	ErrSourceProxyRequired      = errors.New("source proxy is required for relay mode")
	ErrInvalidNodeUpdate        = errors.New("published node update is empty")
	ErrInvalidNodeTopology      = errors.New("published node topology is invalid")
	ErrPublishedNodeReferenced  = errors.New("published node is referenced by a plan")
	ErrPlanNotFound             = errors.New("subscription plan not found")
	ErrInvalidPlanName          = errors.New("subscription plan name must be 1-100 characters")
	ErrInvalidSubscriptionTitle = errors.New("subscription title must not exceed 100 characters")
	ErrInvalidTrafficLimit      = errors.New("subscription plan traffic limit is invalid")
	ErrInvalidTrafficReset      = errors.New("subscription plan traffic reset is invalid")
	ErrInvalidValidityDays      = errors.New("subscription plan validity days is invalid")
	ErrInvalidBillingPeriod     = errors.New("subscription plan billing period is invalid")
	ErrInvalidPlanNodes         = errors.New("subscription plan nodes are invalid")
	ErrPlanReferenced           = errors.New("subscription plan is referenced by a subscriber")
	ErrSubscriberNotFound       = errors.New("subscriber not found")
	ErrInvalidSubscriberPlan    = errors.New("subscriber plan is invalid")
	ErrInvalidSubscriberExpiry  = errors.New("subscriber expiration is invalid")
	ErrSubscriptionNotFound     = errors.New("subscription not found")
	ErrSubscriptionUnavailable  = errors.New("subscription unavailable")
)

type PublishedNode struct {
	ID               int64
	Name             string
	Mode             string
	TargetProxyID    int64
	TargetProxyName  string
	TargetServerID   int64
	TargetServerName string
	SourceProxyID    *int64
	SourceProxyName  string
	SourceServerID   *int64
	SourceServerName string
	RelayID          *int64
	EntryAddress     string
	EntryPort        int
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type CreatePublishedNodeInput struct {
	Name          string
	Mode          string
	TargetProxyID int64
	SourceProxyID *int64
	Enabled       bool
}

type UpdatePublishedNodeInput struct {
	Name    *string
	Enabled *bool
}

type Plan struct {
	ID                  int64
	Name                string
	SubscriptionTitle   string
	Enabled             bool
	TrafficLimitBytes   *int64
	TrafficResetMode    string
	TrafficResetDay     int
	TrafficResetTime    string
	DefaultValidityDays *int
	BillingPeriodMonths *int
	Nodes               []PlanNode
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type PlanNode struct {
	PublishedNode
	Position int
}

type CreatePlanInput struct {
	Name                string
	SubscriptionTitle   string
	Enabled             bool
	TrafficLimitBytes   *int64
	TrafficResetMode    string
	TrafficResetDay     int
	TrafficResetTime    string
	DefaultValidityDays *int
	BillingPeriodMonths *int
}

type UpdatePlanInput struct {
	Name                   *string
	SubscriptionTitle      *string
	Enabled                *bool
	TrafficLimitBytesSet   bool
	TrafficLimitBytes      *int64
	TrafficResetMode       *string
	TrafficResetDay        *int
	TrafficResetTime       *string
	DefaultValidityDaysSet bool
	DefaultValidityDays    *int
	BillingPeriodMonthsSet bool
	BillingPeriodMonths    *int
}

type Subscriber struct {
	UserID              int64
	Username            string
	PlanID              *int64
	PlanName            string
	SubscriptionTitle   string
	PlanEnabled         bool
	ProfileEnabled      bool
	ExpiresAt           *time.Time
	SubscriptionToken   string
	ClientCount         int
	EnabledNodeCount    int
	TrafficLimitBytes   *int64
	UsedBytes           int64
	CycleStartedAt      time.Time
	NextResetAt         *time.Time
	BillingPeriodMonths *int
	Active              bool
	Status              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type UpdateSubscriberInput struct {
	PlanIDSet    bool
	PlanID       *int64
	Enabled      *bool
	ExpiresAtSet bool
	ExpiresAt    *time.Time
}

type GeneratedSubscription struct {
	Title    string
	Body     string
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}

type SubscriptionData struct {
	Title    string
	Nodes    []proxystore.ClientShare
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}

type SubscriberNode struct {
	ID      int64
	Name    string
	Mode    string
	Enabled bool
}
