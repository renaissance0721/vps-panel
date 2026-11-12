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

type SecretStore struct {
	path string
	aad  string
}

var secretMu sync.Mutex

func newSecretStore(path string) *SecretStore { return &SecretStore{path: path, aad: secretAAD} }

func NewMailSecretStore(path string) *SecretStore { return newSecretStore(path) }

// NewRemoteSubscriptionSecretStore uses the backed-up Panel key with a
// separate AEAD domain, so mail ciphertext cannot be swapped with remote data.
func NewRemoteSubscriptionSecretStore(path string) *SecretStore {
	return &SecretStore{path: path, aad: "vps-panel/remote-subscription/v1"}
}

func (s *SecretStore) Encrypt(plaintext string) (string, error) {
	values, err := s.encryptMany([]string{plaintext}, true, "")
	if err != nil {
		return "", err
	}
	return values[0], nil
}

// EncryptExisting refuses to create a key when ciphertext already exists.
func (s *SecretStore) EncryptExisting(plaintext string) (string, error) {
	values, err := s.encryptMany([]string{plaintext}, false, "")
	if err != nil {
		return "", err
	}
	return values[0], nil
}

func (s *SecretStore) EncryptManyExistingChecked(plaintext []string, existingCiphertext string) ([]string, error) {
	if existingCiphertext == "" {
		return nil, errors.New("missing existing ciphertext")
	}
	return s.encryptMany(plaintext, false, existingCiphertext)
}

func (s *SecretStore) encryptMany(plaintext []string, allowCreate bool, existingCiphertext string) ([]string, error) {
	secretMu.Lock()
	defer secretMu.Unlock()
	var key []byte
	var err error
	if allowCreate {
		key, err = s.loadOrCreateKey()
	} else {
		key, err = s.loadKey()
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize mail secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize mail secret AEAD: %w", err)
	}
	if existingCiphertext != "" {
		if !strings.HasPrefix(existingCiphertext, "v1:") {
			return nil, errors.New("unsupported secret format")
		}
		sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(existingCiphertext, "v1:"))
		if err != nil || len(sealed) < aead.NonceSize() {
			return nil, errors.New("invalid encrypted secret")
		}
		if _, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(s.aad)); err != nil {
			return nil, errors.New("authenticate encrypted secret")
		}
	}
	result := make([]string, 0, len(plaintext))
	for _, value := range plaintext {
		nonce := make([]byte, aead.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return nil, fmt.Errorf("generate mail secret nonce: %w", err)
		}
		sealed := aead.Seal(nonce, nonce, []byte(value), []byte(s.aad))
		result = append(result, "v1:"+base64.RawStdEncoding.EncodeToString(sealed))
	}
	return result, nil
}

func (s *SecretStore) Decrypt(ciphertext string) (string, error) {
	values, err := s.DecryptMany([]string{ciphertext})
	if err != nil {
		return "", err
	}
	return values[0], nil
}

func (s *SecretStore) DecryptMany(ciphertexts []string) ([]string, error) {
	secretMu.Lock()
	defer secretMu.Unlock()
	key, err := s.loadKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize mail secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize mail secret AEAD: %w", err)
	}
	values := make([]string, 0, len(ciphertexts))
	for _, ciphertext := range ciphertexts {
		if !strings.HasPrefix(ciphertext, "v1:") {
			return nil, errors.New("unsupported mail secret format")
		}
		sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, "v1:"))
		if err != nil {
			return nil, errors.New("decode mail secret")
		}
		if len(sealed) < aead.NonceSize() {
			return nil, errors.New("mail secret is truncated")
		}
		plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(s.aad))
		if err != nil {
			return nil, errors.New("authenticate mail secret")
		}
		values = append(values, string(plaintext))
	}
	return values, nil
}

func (s *SecretStore) loadOrCreateKey() ([]byte, error) {
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

func (s *SecretStore) loadKey() ([]byte, error) {
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
