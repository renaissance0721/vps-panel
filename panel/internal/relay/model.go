package relay

import (
	"errors"
	"time"
)

const (
	TargetProxy     = "proxy"
	TargetLanding   = "landing"
	TargetManual    = "manual"
	EntryHostAuto   = "auto"
	EntryHostManual = "manual"
	NetworkTCP      = "tcp"
	NetworkUDP      = "udp"
	NetworkBoth     = "tcp,udp"
	maxNameRunes    = 100
)

var (
	ErrNotFound                       = errors.New("relay not found")
	ErrServerNotFound                 = errors.New("server not found")
	ErrProxyNotFound                  = errors.New("target proxy not found")
	ErrLandingNotFound                = errors.New("target landing not found")
	ErrInvalidName                    = errors.New("relay name must be 1-100 characters")
	ErrInvalidListenIP                = errors.New("listen address must be an IP address")
	ErrInvalidPort                    = errors.New("port must be 1-65535")
	ErrInvalidEntryHostMode           = errors.New("entry host mode must be auto or manual")
	ErrInvalidEntryHost               = errors.New("manual entry host must be a hostname or IP address without scheme, path, or port")
	ErrEntryUnavailable               = errors.New("relay entry address is unavailable")
	ErrInvalidTarget                  = errors.New("relay target is invalid")
	ErrInvalidTargetClient            = errors.New("relay target client must belong to target proxy")
	ErrInvalidNetwork                 = errors.New("relay network must be tcp, udp, or tcp,udp")
	ErrPortConflict                   = errors.New("relay listen port conflicts with an existing listener")
	ErrTargetUnavailable              = errors.New("relay target address is unavailable")
	ErrManagedRuntimePurgeUnsupported = errors.New("Agent does not support managed runtime purge")
	ErrServerDecommissioning          = errors.New("server is decommissioning")
)

type Relay struct {
	ID                      int64
	ServerID                int64
	ServerName              string
	ServerPublicIPv4        string
	Name                    string
	ListenAddress           string
	ListenPort              int
	EntryHostMode           string
	EntryHost               string
	EntryAddress            string
	TargetType              string
	TargetProxyID           *int64
	TargetClientID          *int64
	TargetLandingID         *int64
	TargetProxyName         string
	TargetClientName        string
	TargetLandingName       string
	TargetLandingProtocol   string
	TargetLandingVisibility string
	TargetHost              string
	TargetPort              int
	TargetAddressReady      bool
	Network                 string
	Enabled                 bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type CreateInput struct {
	ServerID        int64
	Name            string
	ListenAddress   string
	ListenPort      int
	EntryHostMode   string
	EntryHost       string
	TargetType      string
	TargetProxyID   *int64
	TargetClientID  *int64
	TargetLandingID *int64
	TargetHost      string
	TargetPort      int
	Network         string
	Enabled         bool
}

type UpdateInput struct {
	Name            *string
	ListenAddress   *string
	ListenPort      *int
	EntryHostMode   *string
	EntryHost       *string
	TargetType      *string
	TargetProxyID   *int64
	TargetClientID  *int64
	TargetLandingID *int64
	TargetHost      *string
	TargetPort      *int
	Network         *string
	Enabled         *bool
}

type Mutation struct {
	ServerID int64
	Version  int64
}
