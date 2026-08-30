//go:build windows

package secretstore

import (
	"bytes"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	plain := []byte("omnibridge-dpapi-round-trip-secret")

	ciphertext, err := protect(plain)
	if err != nil {
		t.Fatalf("CryptProtectData: %v", err)
	}
	if len(ciphertext) == 0 {
		t.Fatal("CryptProtectData returned empty ciphertext")
	}
	if bytes.Equal(ciphertext, plain) {
		t.Fatal("DPAPI ciphertext unexpectedly equals plaintext")
	}

	recovered, err := unprotect(ciphertext)
	if err != nil {
		t.Fatalf("CryptUnprotectData: %v", err)
	}
	if !bytes.Equal(recovered, plain) {
		t.Fatalf("DPAPI round-trip mismatch: got %q", recovered)
	}
}

func TestDPAPIRejectsInvalidCiphertext(t *testing.T) {
	if _, err := unprotect([]byte("not-a-valid-dpapi-blob")); err == nil {
		t.Fatal("CryptUnprotectData unexpectedly accepted invalid ciphertext")
	}
}
