package subscription

import (
	"errors"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

const (
	NodeModeDirect               = "direct"
	NodeModeRelay                = "relay"
	EntryHostModeInherit         = "inherit"
	EntryHostModeAuto            = "auto"
	EntryHostModeManual          = "manual"
	EntryPortModeInherit         = "inherit"
	EntryPortModeAuto            = "auto"
	EntryPortModeManual          = "manual"
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
	ErrSourceServerNotFound     = errors.New("source server not found")
	ErrSourceServerRequired     = errors.New("source server is required for relay mode")
	ErrInvalidNodeUpdate        = errors.New("published node update is empty")
	ErrInvalidNodeTopology      = errors.New("published node topology is invalid")
	ErrInvalidEntryHostMode     = errors.New("published node entry host mode is invalid")
	ErrInvalidEntryPortMode     = errors.New("published node entry port mode is invalid")
	ErrInvalidTrafficMultiplier = errors.New("published node traffic multiplier is invalid")
	ErrPublishedNodeReferenced  = errors.New("published node is referenced by a plan")
	ErrPlanNotFound             = errors.New("subscription plan not found")
	ErrInvalidPlanName          = errors.New("subscription plan name must be 1-100 characters")
	ErrInvalidSubscriptionTitle = errors.New("subscription title must not exceed 100 characters")
	ErrInvalidTrafficLimit      = errors.New("subscription plan traffic limit is invalid")
	ErrInvalidTrafficReset      = errors.New("subscriber traffic reset is invalid")
	ErrInvalidBillingPeriod     = errors.New("subscriber billing period is invalid")
	ErrInvalidPlanNodes         = errors.New("subscription plan nodes are invalid")
	ErrPlanReferenced           = errors.New("subscription plan is referenced by a subscriber")
	ErrSubscriberNotFound       = errors.New("subscriber not found")
	ErrInvalidSubscriberPlan    = errors.New("subscriber plan is invalid")
	ErrInvalidSubscriberExpiry  = errors.New("subscriber expiration is invalid")
	ErrSubscriptionNotFound     = errors.New("subscription not found")
	ErrSubscriptionUnavailable  = errors.New("subscription unavailable")
	ErrServerNotDistributable   = errors.New("published nodes require administrator-created servers")
	ErrRoutingPresetNotFound    = errors.New("subscription routing preset not found")
	ErrTemplateNotFound         = errors.New("subscription template not found")
	ErrInvalidRoutingPreset     = errors.New("subscription routing preset is invalid")
	ErrInvalidPlanRouting       = errors.New("subscription plan routing is invalid")
	ErrInvalidTemplate          = errors.New("subscription template is invalid")
	ErrTemplateReferenced       = errors.New("subscription template is referenced by a plan")
)

type PublishedNode struct {
	ID                  int64
	Name                string
	Mode                string
	TargetProxyID       int64
	TargetProxyName     string
	TargetServerID      int64
	TargetServerName    string
	SourceServerID      *int64
	SourceServerName    string
	RelayID             *int64
	EntryHostMode       string
	EntryHost           string
	EntryPortMode       string
	TrafficMultiplierBP int
	EntryAddress        string
	EntryPort           int
	Enabled             bool
	Distributable       bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CreatePublishedNodeInput struct {
	Name                string
	Mode                string
	TargetProxyID       int64
	SourceServerID      *int64
	EntryHostMode       string
	EntryHost           string
	EntryPortMode       string
	EntryPort           *int
	PlanIDs             []int64
	TrafficMultiplierBP int
	Enabled             bool
}

type UpdatePublishedNodeInput struct {
	Name                *string
	EntryHostMode       *string
	EntryHost           *string
	EntryPortMode       *string
	EntryPort           *int
	TrafficMultiplierBP *int
	Enabled             *bool
	PlanIDsSet          bool
	PlanIDs             []int64
}

type Plan struct {
	ID                int64
	Name              string
	SubscriptionTitle string
	Enabled           bool
	TrafficLimitBytes *int64
	RoutingGroups     []RoutingGroup
	RoutingRules      []string
	TemplateID        *int64
	Nodes             []PlanNode
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type PlanNode struct {
	PublishedNode
	Position int
}

type CreatePlanInput struct {
	Name              string
	SubscriptionTitle string
	Enabled           bool
	TrafficLimitBytes *int64
	RoutingGroups     []RoutingGroup
	RoutingRules      []string
	TemplateID        *int64
}

type UpdatePlanInput struct {
	Name                 *string
	SubscriptionTitle    *string
	Enabled              *bool
	TrafficLimitBytesSet bool
	TrafficLimitBytes    *int64
	RoutingGroupsSet     bool
	RoutingGroups        []RoutingGroup
	RoutingRulesSet      bool
	RoutingRules         []string
	TemplateIDSet        bool
	TemplateID           *int64
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
	TrafficResetMode    string
	TrafficResetDay     int
	TrafficResetTime    string
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
	PlanIDSet              bool
	PlanID                 *int64
	Enabled                *bool
	ExpiresAtSet           bool
	ExpiresAt              *time.Time
	TrafficResetMode       *string
	TrafficResetDay        *int
	TrafficResetTime       *string
	BillingPeriodMonthsSet bool
	BillingPeriodMonths    *int
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
	Title              string
	Nodes              []proxystore.ClientShare
	Upload             int64
	Download           int64
	Total              int64
	Expire             int64
	PublishedNodeNames map[int64]string
	RoutingGroups      []RoutingGroup
	RoutingRules       []string
	Template           *SubscriptionTemplate
}

type RoutingPreset struct {
	ID        int64
	Name      string
	Enabled   bool
	Groups    []RoutingGroup
	Rules     []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type RoutingGroup struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Proxies    []string `json:"proxies"`
	NodeIDs    []int64  `json:"node_ids,omitempty"`
	IncludeAll bool     `json:"include_all,omitempty"`
}

type CreateRoutingPresetInput struct {
	Name    string
	Enabled bool
	Groups  []RoutingGroup
	Rules   []string
}

type UpdateRoutingPresetInput struct {
	Name    *string
	Enabled *bool
	Groups  *[]RoutingGroup
	Rules   *[]string
}

type SubscriptionTemplate struct {
	ID         int64
	Name       string
	Enabled    bool
	ConfigYAML string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type CreateSubscriptionTemplateInput struct {
	Name       string
	Enabled    bool
	ConfigYAML string
}

type UpdateSubscriptionTemplateInput struct {
	Name       *string
	Enabled    *bool
	ConfigYAML *string
}

type SubscriberNode struct {
	ID                  int64
	Name                string
	Mode                string
	TrafficMultiplierBP int
	Enabled             bool
}
