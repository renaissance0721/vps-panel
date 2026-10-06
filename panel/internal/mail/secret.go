package mail

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	KeyFileName = "mail.key"
	keySize     = 32
	secretAAD   = "vps-panel/mail-settings/password/v1"
)

type secretStore struct {
	path string
	mu   sync.Mutex
}

func newSecretStore(path string) *secretStore { return &secretStore{path: path} }

func (s *secretStore) Encrypt(plaintext string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := s.loadOrCreateKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("initialize mail secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("initialize mail secret AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate mail secret nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte(secretAAD))
	return "v1:" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *secretStore) Decrypt(ciphertext string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := s.loadKey()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(ciphertext, "v1:") {
		return "", errors.New("unsupported mail secret format")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, "v1:"))
	if err != nil {
		return "", errors.New("decode mail secret")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("initialize mail secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("initialize mail secret AEAD: %w", err)
	}
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("mail secret is truncated")
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(secretAAD))
	if err != nil {
		return "", errors.New("authenticate mail secret")
	}
	return string(plaintext), nil
}

func (s *secretStore) loadOrCreateKey() ([]byte, error) {
	key, err := s.loadKey()
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.path == "" {
		return nil, errors.New("mail secret key path is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return nil, fmt.Errorf("prepare mail secret directory: %w", err)
	}
	key = make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate mail secret key: %w", err)
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return s.loadKey()
	}
	if err != nil {
		return nil, fmt.Errorf("create mail secret key: %w", err)
	}
	_, writeErr := file.Write(key)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(s.path)
		return nil, errors.New("write mail secret key")
	}
	return key, nil
}

func (s *secretStore) loadKey() ([]byte, error) {
	if s.path == "" {
		return nil, os.ErrNotExist
	}
	info, err := os.Lstat(s.path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("mail secret key is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("mail secret key permissions are too broad")
	}
	key, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read mail secret key: %w", err)
	}
	if len(key) != keySize {
		return nil, errors.New("mail secret key has invalid length")
	}
	return key, nil
}
