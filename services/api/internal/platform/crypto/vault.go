package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

var ErrInvalidMasterKey = errors.New("master key must contain exactly 32 bytes")

type Vault struct {
	aead cipher.AEAD
}

func NewVault(masterKey []byte) (*Vault, error) {
	if len(masterKey) != 32 {
		return nil, ErrInvalidMasterKey
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Encrypt(plaintext, associatedData []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("create nonce: %w", err)
	}
	return v.aead.Seal(nil, nonce, plaintext, associatedData), nonce, nil
}

func (v *Vault) Decrypt(ciphertext, nonce, associatedData []byte) ([]byte, error) {
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, errors.New("decrypt protected value")
	}
	return plaintext, nil
}
