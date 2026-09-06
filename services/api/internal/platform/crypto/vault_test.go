package crypto

import (
	"bytes"
	"testing"
)

func TestVaultRoundTripAndContextBinding(t *testing.T) {
	vault, err := NewVault(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := vault.Encrypt([]byte("refresh-token"), []byte("account-id"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := vault.Decrypt(ciphertext, nonce, []byte("account-id"))
	if err != nil || string(plaintext) != "refresh-token" {
		t.Fatalf("round trip failed: plaintext=%q err=%v", plaintext, err)
	}
	if _, err := vault.Decrypt(ciphertext, nonce, []byte("another-account")); err == nil {
		t.Fatal("expected associated data mismatch to fail")
	}
}
