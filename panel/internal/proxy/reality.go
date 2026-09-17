package proxy

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

type storedReality struct {
	Target     string `json:"target"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

func newRealitySecrets() (string, string, string, error) {
	privateBytes := make([]byte, 32)
	if _, err := rand.Read(privateBytes); err != nil {
		return "", "", "", fmt.Errorf("generate REALITY private key: %w", err)
	}
	privateBytes[0] &= 248
	privateBytes[31] &= 127
	privateBytes[31] |= 64
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return "", "", "", fmt.Errorf("generate REALITY key pair: %w", err)
	}
	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return "", "", "", fmt.Errorf("generate REALITY short id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(privateBytes),
		base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()), hex.EncodeToString(shortID), nil
}

func normalizeTarget(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/?#@") {
		return "", ErrInvalidReality
	}
	host, portValue, err := net.SplitHostPort(value)
	if err != nil {
		return "", ErrInvalidReality
	}
	host, err = normalizeHost(host, false)
	if err != nil {
		return "", ErrInvalidReality
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || validatePort(port) != nil {
		return "", ErrInvalidReality
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func validateReality(value *storedReality) error {
	if _, err := normalizeTarget(value.Target); err != nil {
		return err
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(value.PrivateKey)
	if err != nil || len(privateBytes) != 32 {
		return ErrInvalidReality
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil || base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()) != value.PublicKey {
		return ErrInvalidReality
	}
	shortID, err := hex.DecodeString(value.ShortID)
	if err != nil || len(shortID) != 8 {
		return ErrInvalidReality
	}
	return nil
}
