// Package secret provides AES-GCM encryption for Secret resources.
//
// Secrets are stored as ciphertext + nonce in the KV store. Only
// holders of the master key can decrypt. The master key is provided
// at startup (typically loaded from disk or env var).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Errors.
var (
	ErrCipherTextTooShort = errors.New("secret: ciphertext too short")
	ErrDecryptFailed      = errors.New("secret: decrypt failed")
)

// Cipher encrypts and decrypts Secret values.
type Cipher struct {
	gcm cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte master key.
func NewCipher(masterKey []byte) (*Cipher, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("secret: master key must be 32 bytes, got %d", len(masterKey))
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{gcm: gcm}, nil
}

// Encrypt produces nonce || ciphertext.
func (c *Cipher) Encrypt(plaintext []byte) (ct []byte, nonce []byte, err error) {
	n := make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, nil, err
	}
	ct = c.gcm.Seal(nil, n, plaintext, nil)
	return ct, n, nil
}

// Decrypt expects nonce || ciphertext.
func (c *Cipher) Decrypt(ct, nonce []byte) ([]byte, error) {
	if len(nonce) != c.gcm.NonceSize() {
		return nil, ErrCipherTextTooShort
	}
	pt, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return pt, nil
}

// GenerateKey returns a new 32-byte key suitable for NewCipher.
func GenerateKey() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
