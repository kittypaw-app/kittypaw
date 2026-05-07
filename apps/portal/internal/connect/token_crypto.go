package connect

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

const connectTokenKeyBytes = 32

type TokenCipher struct {
	gcm cipher.AEAD
}

func NewTokenCipher(key []byte) (*TokenCipher, error) {
	if len(key) != connectTokenKeyBytes {
		return nil, fmt.Errorf("connect token encryption key must be %d bytes, got %d", connectTokenKeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("connect token cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("connect token gcm: %w", err)
	}
	return &TokenCipher{gcm: gcm}, nil
}

func (c *TokenCipher) Encrypt(plaintext string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("connect token cipher unavailable")
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("connect token nonce: %w", err)
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+c.gcm.Overhead())
	out = append(out, nonce...)
	out = c.gcm.Seal(out, nonce, []byte(plaintext), nil)
	return out, nil
}

func (c *TokenCipher) Decrypt(ciphertext []byte) (string, error) {
	if c == nil {
		return "", fmt.Errorf("connect token cipher unavailable")
	}
	nonceSize := c.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("connect token ciphertext too short")
	}
	nonce := ciphertext[:nonceSize]
	body := ciphertext[nonceSize:]
	plaintext, err := c.gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("connect token decrypt: %w", err)
	}
	return string(plaintext), nil
}
