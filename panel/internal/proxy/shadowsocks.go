package proxy

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	ShadowsocksNetwork         = "tcp,udp"
	ShadowsocksMethodAES128GCM = "2022-blake3-aes-128-gcm"
	ShadowsocksMethodAES256GCM = "2022-blake3-aes-256-gcm"
)

type storedShadowsocks struct {
	Method   string `json:"method"`
	Network  string `json:"network"`
	Password string `json:"password"`
}

func newShadowsocksConfigAndCredential(method string) (storedConfig, storedCredential, error) {
	method, err := normalizeShadowsocksMethod(method)
	if err != nil {
		return storedConfig{}, storedCredential{}, err
	}
	masterPassword, err := newShadowsocksKey(method)
	if err != nil {
		return storedConfig{}, storedCredential{}, err
	}
	clientPassword, err := newShadowsocksKey(method)
	if err != nil {
		return storedConfig{}, storedCredential{}, err
	}
	return storedConfig{Shadowsocks: &storedShadowsocks{
		Method: method, Network: ShadowsocksNetwork, Password: masterPassword,
	}}, storedCredential{Password: clientPassword}, nil
}

func newShadowsocksKey(method string) (string, error) {
	length, err := shadowsocksKeyLength(method)
	if err != nil {
		return "", err
	}
	value := make([]byte, length)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Shadowsocks key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(value), nil
}

func normalizeShadowsocksMethod(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ShadowsocksMethodAES128GCM, nil
	}
	if _, err := shadowsocksKeyLength(value); err != nil {
		return "", err
	}
	return value, nil
}

func shadowsocksKeyLength(method string) (int, error) {
	switch method {
	case ShadowsocksMethodAES128GCM:
		return 16, nil
	case ShadowsocksMethodAES256GCM:
		return 32, nil
	default:
		return 0, ErrInvalidShadowsocksMethod
	}
}

func validShadowsocksKey(value, method string) bool {
	expected, err := shadowsocksKeyLength(method)
	if err != nil {
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == expected
}

func buildShadowsocksURI(share ClientShare, masterPassword string) string {
	return (&url.URL{
		Scheme:   "ss",
		User:     url.UserPassword(share.Method, masterPassword+":"+share.Password),
		Host:     net.JoinHostPort(share.Address, strconv.Itoa(share.Port)),
		Fragment: share.ProxyName + " - " + share.Name,
	}).String()
}
