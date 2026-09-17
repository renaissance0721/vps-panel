package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func newStoredConfig(security, serverName, tlsMode, certificate, privateKey, realityTarget string) (storedConfig, error) {
	security = strings.ToLower(strings.TrimSpace(security))
	serverName, err := normalizeHost(serverName, false)
	if err != nil {
		return storedConfig{}, ErrInvalidServerName
	}
	config := storedConfig{Transport: TransportTCP, Security: security, ServerFlow: ServerFlow, ServerName: serverName, Fingerprint: Fingerprint}
	switch security {
	case SecurityTLS:
		mode, err := normalizeTLSMode(tlsMode, certificate, privateKey)
		if err != nil {
			return storedConfig{}, err
		}
		if err := validateTLSConfig(mode, serverName, certificate, privateKey); err != nil {
			return storedConfig{}, err
		}
		config.TLS = &storedTLS{Mode: mode, Certificate: strings.TrimSpace(certificate), PrivateKey: strings.TrimSpace(privateKey)}
	case SecurityReality:
		target, err := normalizeTarget(realityTarget)
		if err != nil {
			return storedConfig{}, err
		}
		privateValue, publicValue, shortID, err := newRealitySecrets()
		if err != nil {
			return storedConfig{}, err
		}
		config.Reality = &storedReality{Target: target, PrivateKey: privateValue, PublicKey: publicValue, ShortID: shortID}
	default:
		return storedConfig{}, ErrInvalidSecurity
	}
	return config, nil
}

func updateStoredConfig(config *storedConfig, input UpdateInput) error {
	security := config.Security
	if input.Security != nil {
		security = strings.ToLower(strings.TrimSpace(*input.Security))
	}
	serverName := config.ServerName
	if input.ServerName != nil {
		var err error
		serverName, err = normalizeHost(*input.ServerName, false)
		if err != nil {
			return ErrInvalidServerName
		}
	}
	switch security {
	case SecurityTLS:
		certificate, privateKey := "", ""
		mode := ""
		if config.Security == SecurityTLS && config.TLS != nil {
			certificate, privateKey = config.TLS.Certificate, config.TLS.PrivateKey
			mode = config.TLS.Mode
		}
		if input.TLSMode != nil {
			mode = *input.TLSMode
		}
		providedCert, providedKey := input.Certificate != nil && strings.TrimSpace(*input.Certificate) != "", input.PrivateKey != nil && strings.TrimSpace(*input.PrivateKey) != ""
		if providedCert != providedKey {
			return ErrInvalidTLS
		}
		if providedCert {
			certificate, privateKey = *input.Certificate, *input.PrivateKey
		}
		mode, err := normalizeTLSMode(mode, certificate, privateKey)
		if err != nil {
			return err
		}
		if mode == TLSModeACME && !providedCert {
			certificate, privateKey = "", ""
		}
		if err := validateTLSConfig(mode, serverName, certificate, privateKey); err != nil {
			return err
		}
		*config = storedConfig{Transport: TransportTCP, Security: SecurityTLS, ServerFlow: ServerFlow, ServerName: serverName, Fingerprint: Fingerprint, TLS: &storedTLS{Mode: mode, Certificate: strings.TrimSpace(certificate), PrivateKey: strings.TrimSpace(privateKey)}}
	case SecurityReality:
		target := ""
		var reality *storedReality
		if config.Security == SecurityReality && config.Reality != nil {
			copyValue := *config.Reality
			reality = &copyValue
			target = reality.Target
		} else {
			privateValue, publicValue, shortID, err := newRealitySecrets()
			if err != nil {
				return err
			}
			reality = &storedReality{PrivateKey: privateValue, PublicKey: publicValue, ShortID: shortID}
		}
		if input.RealityTarget != nil {
			target = *input.RealityTarget
		}
		var err error
		reality.Target, err = normalizeTarget(target)
		if err != nil {
			return err
		}
		*config = storedConfig{Transport: TransportTCP, Security: SecurityReality, ServerFlow: ServerFlow, ServerName: serverName, Fingerprint: Fingerprint, Reality: reality}
	default:
		return ErrInvalidSecurity
	}
	return nil
}

func decodeConfig(protocol, value string) (storedConfig, error) {
	var config storedConfig
	if err := decodeStrict(value, &config); err != nil {
		return storedConfig{}, fmt.Errorf("decode proxy config: %w", err)
	}
	if protocol == ProtocolShadowsocks {
		if config.Shadowsocks == nil || config.Transport != "" || config.Security != "" || config.ServerFlow != "" ||
			config.ServerName != "" || config.Fingerprint != "" || config.TLS != nil || config.Reality != nil ||
			config.Shadowsocks.Network != ShadowsocksNetwork ||
			!validShadowsocksKey(config.Shadowsocks.Password, config.Shadowsocks.Method) {
			return storedConfig{}, errors.New("invalid stored Shadowsocks proxy config")
		}
		return config, nil
	}
	if protocol != ProtocolVLESS || config.Shadowsocks != nil {
		return storedConfig{}, errors.New("invalid stored proxy protocol config")
	}
	if config.Transport != TransportTCP || config.ServerFlow != ServerFlow || config.Fingerprint != Fingerprint {
		return storedConfig{}, errors.New("invalid stored proxy config")
	}
	serverName, err := normalizeHost(config.ServerName, false)
	if err != nil || serverName != config.ServerName {
		return storedConfig{}, errors.New("invalid stored proxy server name")
	}
	switch config.Security {
	case SecurityTLS:
		if config.TLS == nil || config.Reality != nil {
			return storedConfig{}, errors.New("invalid stored TLS proxy config")
		}
		mode, modeErr := normalizeTLSMode(config.TLS.Mode, config.TLS.Certificate, config.TLS.PrivateKey)
		if modeErr != nil || validateTLSConfig(mode, config.ServerName, config.TLS.Certificate, config.TLS.PrivateKey) != nil {
			return storedConfig{}, errors.New("invalid stored TLS proxy config")
		}
		config.TLS.Mode = mode
	case SecurityReality:
		if config.Reality == nil || config.TLS != nil || validateReality(config.Reality) != nil {
			return storedConfig{}, errors.New("invalid stored REALITY proxy config")
		}
	default:
		return storedConfig{}, errors.New("invalid stored proxy security")
	}
	return config, nil
}

func decodeStrict(value string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func publicConfig(config storedConfig) PublicConfig {
	value := PublicConfig{Transport: config.Transport, Security: config.Security, ServerFlow: config.ServerFlow, ServerName: config.ServerName, Fingerprint: config.Fingerprint}
	if config.Shadowsocks != nil {
		value.Method = config.Shadowsocks.Method
		value.Network = config.Shadowsocks.Network
	}
	if config.TLS != nil {
		value.TLSMode = config.TLS.Mode
		value.TLSCertificateConfigured = config.TLS.Certificate != "" && config.TLS.PrivateKey != ""
	}
	if config.Reality != nil {
		value.RealityTarget = config.Reality.Target
	}
	return value
}
