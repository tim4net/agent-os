// Package secret provides authenticated symmetric encryption (AES-256-GCM)
// for secrets stored at rest in the app_settings table, plus a master-key
// resolver that survives container redeploys.
//
// Threat model: protect provider API keys / agent auth tokens so a DB dump
// (volume snapshot, pg_dump, backup leak) does not expose plaintext secrets.
// The master key lives OUTSIDE the database — in AOS_MASTER_KEY (preferred,
// injected via the systemd EnvironmentFile) or, as a fallback, generated once
// to a file on the persistent artifacts volume. The cipher is nil when no key
// is available; callers MUST treat a nil Cipher as "secrets disabled" and
// refuse to store secrets rather than persisting plaintext.
package secret

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
)

// ErrNoKey is returned when encryption is requested but no master key is set.
var ErrNoKey = errors.New("secret: no master key configured")

// ErrMalformed is returned when ciphertext is too short or corrupt.
var ErrMalformed = errors.New("secret: malformed ciphertext")

// Cipher encrypts and decrypts secret values with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte key. Any other length is rejected
// so we never silently truncate/pad a misconfigured key into a weak one.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce-prefixed ciphertext: nonce || GCM(plaintext).
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrNoKey
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt reverses Encrypt. Returns ErrMalformed if the input is too short.
func (c *Cipher) Decrypt(ciphertext []byte) (string, error) {
	if c == nil || c.aead == nil {
		return "", ErrNoKey
	}
	ns := c.aead.NonceSize()
	if len(ciphertext) < ns {
		return "", ErrMalformed
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	plain, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("secret: open: %w", err)
	}
	return string(plain), nil
}

// Enabled reports whether the cipher can actually encrypt.
func (c *Cipher) Enabled() bool {
	return c != nil && c.aead != nil
}

// ResolveMasterKey returns a 32-byte key from, in order of preference:
//  1. AOS_MASTER_KEY env var (base64 std or raw 32-byte string)
//  2. a key file at <fallbackDir>/master.key — read if present, else GENERATED
//     once (0600) so secrets survive redeploys even without an env key.
//
// Returns (nil, nil) only when no env key is set AND fallbackDir is empty,
// signaling "secrets disabled" to the caller (no plaintext fallback ever).
func ResolveMasterKey(fallbackDir string) ([]byte, error) {
	if env := os.Getenv("AOS_MASTER_KEY"); env != "" {
		return decodeKey(env)
	}
	if fallbackDir == "" {
		return nil, nil
	}
	keyPath := filepath.Join(fallbackDir, "master.key")
	if b, err := os.ReadFile(keyPath); err == nil {
		return decodeKey(string(b))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("secret: read key file: %w", err)
	}
	// Generate once and persist.
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("secret: generate key: %w", err)
	}
	if err := os.MkdirAll(fallbackDir, 0o700); err != nil {
		return nil, fmt.Errorf("secret: mkdir key dir: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("secret: write key file: %w", err)
	}
	return key, nil
}

// decodeKey accepts a base64-encoded 32-byte key or a raw 32-byte string.
func decodeKey(s string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("secret: AOS_MASTER_KEY must be 32 raw bytes or base64 of 32 bytes (got %d chars)", len(s))
}

// Last4 returns the last 4 characters of a secret for masked display.
// Shorter secrets are reported in full to avoid implying more entropy.
func Last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
}
