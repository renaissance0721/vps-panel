package proxy

import (
	"crypto/tls"
	"net"
	"strings"
)

const (
	TLSModeACME   = "acme"
	TLSModeManual = "manual"
)

type storedTLS struct {
	Mode        string `json:"mode,omitempty"`
	Certificate string `json:"certificate,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
}

func validateTLS(certificate, privateKey string) error {
	if strings.TrimSpace(certificate) == "" || strings.TrimSpace(privateKey) == "" {
		return ErrInvalidTLS
	}
	if _, err := tls.X509KeyPair([]byte(certificate), []byte(privateKey)); err != nil {
		return ErrInvalidTLS
	}
	return nil
}

func normalizeTLSMode(mode, certificate, privateKey string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if strings.TrimSpace(certificate) != "" || strings.TrimSpace(privateKey) != "" {
			return TLSModeManual, nil
		}
		return TLSModeACME, nil
	}
	if mode != TLSModeACME && mode != TLSModeManual {
		return "", ErrInvalidTLSMode
	}
	return mode, nil
}

func validateTLSConfig(mode, serverName, certificate, privateKey string) error {
	if mode == TLSModeACME {
		if certificate != "" || privateKey != "" {
			return ErrInvalidTLS
		}
		if !validACMEDomain(serverName) {
			return ErrInvalidACMEDomain
		}
		return nil
	}
	return validateTLS(certificate, privateKey)
}

func validACMEDomain(domain string) bool {
	if net.ParseIP(domain) != nil || len(domain) > 253 || strings.ToLower(domain) != domain || !strings.Contains(domain, ".") {
		return false
	}
	if normalized, err := normalizeHost(domain, false); err != nil || normalized != domain {
		return false
	}
	parts := strings.Split(domain, ".")
	tld := parts[len(parts)-1]
	if len(tld) < 2 || tld == "local" || tld == "localhost" {
		return false
	}
	if !strings.HasPrefix(tld, "xn--") {
		for _, character := range tld {
			if character < 'a' || character > 'z' {
				return false
			}
		}
	}
	return true
}
