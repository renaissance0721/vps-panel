package main

import (
	"bytes"
	"testing"
)

func TestWindowsDPAPIProtectsProfile(t *testing.T) {
	secret := []byte("synthetic connector credential")
	cipher, err := protectProfile(secret, false)
	if err != nil || bytes.Contains(cipher, secret) {
		t.Fatalf("protect profile: %v", err)
	}
	plain, err := protectProfile(cipher, true)
	if err != nil || !bytes.Equal(plain, secret) {
		t.Fatalf("unprotect profile: %v", err)
	}
	if _, err := protectProfile(cipher[:len(cipher)/2], true); err == nil {
		t.Fatal("truncated DPAPI profile was accepted")
	}
}
