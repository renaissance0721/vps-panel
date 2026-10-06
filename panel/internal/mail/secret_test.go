package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretStoreAuthenticatedEncryptionAndPersistence(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), KeyFileName)
	store := newSecretStore(keyPath)
	first, err := store.Encrypt("smtp-secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Encrypt("smtp-secret")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || strings.Contains(first, "smtp-secret") {
		t.Fatalf("ciphertexts are not randomized or exposed plaintext: %q / %q", first, second)
	}
	restarted := newSecretStore(keyPath)
	plaintext, err := restarted.Decrypt(first)
	if err != nil || plaintext != "smtp-secret" {
		t.Fatalf("decrypt after restart = %q, %v", plaintext, err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || len(key) != keySize {
		t.Fatalf("key length = %d, %v", len(key), err)
	}

	tampered := first[:len(first)-1] + "A"
	if tampered == first {
		tampered = first[:len(first)-1] + "B"
	}
	if _, err := restarted.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}

	wrongKeyPath := filepath.Join(t.TempDir(), KeyFileName)
	wrong := newSecretStore(wrongKeyPath)
	if _, err := wrong.Encrypt("different"); err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Decrypt(first); err == nil {
		t.Fatal("ciphertext decrypted with a different key")
	}
}
