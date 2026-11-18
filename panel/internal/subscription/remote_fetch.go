package subscription

import "github.com/renaissance0721/vps-panel/panel/internal/subscriptionfetch"

const remoteSubscriptionMaxBytes = subscriptionfetch.MaxBytes

var (
	errRemoteURL      = subscriptionfetch.ErrURL
	errRemoteFetch    = subscriptionfetch.ErrFetch
	errRemoteStatus   = subscriptionfetch.ErrStatus
	errRemoteTooLarge = subscriptionfetch.ErrTooLarge
	errRemoteTimeout  = subscriptionfetch.ErrTimeout
)
