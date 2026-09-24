package landing

import (
	"errors"
	"time"
)

const (
	VisibilityPrivate = "private"
	VisibilityPublic  = "public"
	ProtocolVLESS     = "vless"
	ProtocolSS        = "shadowsocks"
	maxNameRunes      = 100
	maxURIBytes       = 16 * 1024
)

var (
	ErrNotFound                  = errors.New("landing not found")
	ErrInvalidName               = errors.New("landing name must be 1-100 characters")
	ErrInvalidVisibility         = errors.New("landing visibility must be private or public")
	ErrInvalidURI                = errors.New("invalid landing URI")
	ErrUnsupportedProtocol       = errors.New("only VLESS and Shadowsocks landings are supported")
	ErrUnsupportedVLESSTransport = errors.New("only TCP VLESS landings are supported")
	ErrUnsupportedSSPlugin       = errors.New("Shadowsocks plugins are not supported")
	ErrImmutableProtocol         = errors.New("landing protocol is immutable")
	ErrReferencedByRelay         = errors.New("landing is referenced by a relay")
)

type Landing struct {
	ID          int64
	OwnerUserID int64
	Name        string
	Visibility  string
	Protocol    string
	Host        string
	Port        int
	OwnedByMe   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ParsedURI struct {
	Protocol string
	Host     string
	Port     int
	Fragment string
}

type CreateInput struct {
	Name       string
	Visibility string
	URI        string
}

type UpdateInput struct {
	Name       *string
	Visibility *string
	URI        *string
}

type ShareEndpoint struct {
	Address string
	Port    int
}
