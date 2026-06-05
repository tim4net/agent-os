package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	for _, pt := range []string{"", "sk-ant-abc123", "a", "tok_" + string(make([]byte, 200))} {
		ct, err := c.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", pt, err)
		}
		got, err := c.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != pt {
			t.Fatalf("round trip mismatch: got %q want %q", got, pt)
		}
	}
}

func TestCiphertextIsNotPlaintext(t *testing.T) {
	c := newTestCipher(t)
	pt := "super-secret-api-key"
	ct, err := c.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ct, []byte(pt)) {
		t.Fatal("ciphertext contains plaintext — encryption is not happening")
	}
}

func TestNonceIsRandomPerCall(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext — nonce reuse")
	}
}

func TestDecryptRejectsTamper(t *testing.T) {
	c := newTestCipher(t)
	ct, _ := c.Encrypt("integrity-protected")
	ct[len(ct)-1] ^= 0xFF // flip a bit in the tag/ciphertext
	if _, err := c.Decrypt(ct); err == nil {
		t.Fatal("GCM accepted tampered ciphertext — authentication broken")
	}
}

func TestDecryptRejectsShort(t *testing.T) {
	c := newTestCipher(t)
	if _, err := c.Decrypt([]byte{1, 2, 3}); err != ErrMalformed {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
}

func TestNewCipherRejectsBadKeyLen(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewCipher(make([]byte, n)); err == nil {
			t.Fatalf("NewCipher accepted %d-byte key (must require 32)", n)
		}
	}
}

func TestNilCipherDisabled(t *testing.T) {
	var c *Cipher
	if c.Enabled() {
		t.Fatal("nil cipher reports Enabled")
	}
	if _, err := c.Encrypt("x"); err != ErrNoKey {
		t.Fatalf("nil cipher Encrypt: want ErrNoKey, got %v", err)
	}
}

func TestResolveMasterKeyFromEnvBase64(t *testing.T) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AOS_MASTER_KEY", base64.StdEncoding.EncodeToString(raw))
	got, err := ResolveMasterKey("")
	if err != nil {
		t.Fatalf("ResolveMasterKey: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("env base64 key not decoded correctly")
	}
}

func TestResolveMasterKeyFromEnvRaw32(t *testing.T) {
	raw := "01234567890123456789012345678901" // exactly 32 chars
	t.Setenv("AOS_MASTER_KEY", raw)
	got, err := ResolveMasterKey("")
	if err != nil {
		t.Fatalf("ResolveMasterKey: %v", err)
	}
	if string(got) != raw {
		t.Fatal("raw 32-byte key not returned")
	}
}

func TestResolveMasterKeyDisabledWhenEmpty(t *testing.T) {
	t.Setenv("AOS_MASTER_KEY", "")
	got, err := ResolveMasterKey("")
	if err != nil {
		t.Fatalf("ResolveMasterKey: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil key (secrets disabled) when no env and no fallback dir")
	}
}

func TestResolveMasterKeyFileFallbackGeneratesAndPersists(t *testing.T) {
	t.Setenv("AOS_MASTER_KEY", "")
	dir := t.TempDir()
	first, err := ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("generated key wrong length: %d", len(first))
	}
	// File created with 0600.
	info, err := os.Stat(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file perm = %o, want 600", perm)
	}
	// Second resolve returns the SAME key (persisted, survives "redeploy").
	second, err := ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("key not stable across resolves — secrets would break on redeploy")
	}
}

func TestLast4(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"ab":           "",
		"abcd":         "", // <=4 chars: masked entirely so plaintext never leaks
		"sk-0123456":   "3456",
		"longer-token": "oken",
	}
	for in, want := range cases {
		if got := Last4(in); got != want {
			t.Fatalf("Last4(%q) = %q, want %q", in, got, want)
		}
	}
}
