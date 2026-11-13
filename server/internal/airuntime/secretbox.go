package airuntime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

var ErrEncryptionKeyUnavailable = errors.New("TIANQIONG_AI_ENCRYPTION_KEY must contain a base64-encoded 32-byte key")

type SecretBox struct{}

func (SecretBox) key() ([]byte, error) {
	encoded := strings.TrimSpace(os.Getenv("TIANQIONG_AI_ENCRYPTION_KEY"))
	keyFile := strings.TrimSpace(os.Getenv("TIANQIONG_AI_ENCRYPTION_KEY_FILE"))
	if keyFile != "" {
		if encoded != "" {
			return nil, ErrEncryptionKeyUnavailable
		}
		contents, err := os.ReadFile(keyFile)
		if err != nil || len(contents) > 4096 {
			return nil, ErrEncryptionKeyUnavailable
		}
		encoded = strings.TrimSpace(string(contents))
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, ErrEncryptionKeyUnavailable
	}
	return key, nil
}

func (s SecretBox) aead() (cipher.AEAD, error) {
	key, err := s.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s SecretBox) Fingerprint(scope string, payload []byte) (string, error) {
	key, err := s.key()
	if err != nil {
		return "", err
	}
	derive := hmac.New(sha256.New, key)
	_, _ = derive.Write([]byte("tianqiong/asset-fingerprint/v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	_, _ = mac.Write([]byte(scope))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil)), nil
}

func (s SecretBox) Seal(scope, secret string) ([]byte, error) {
	aead, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(secret), []byte(scope)), nil
}

func (s SecretBox) Open(scope string, encrypted []byte) (string, error) {
	aead, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(encrypted) < aead.NonceSize() {
		return "", errors.New("invalid encrypted AI credential")
	}
	nonce, ciphertext := encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(scope))
	if err != nil {
		return "", errors.New("cannot decrypt AI credential")
	}
	return string(plaintext), nil
}
