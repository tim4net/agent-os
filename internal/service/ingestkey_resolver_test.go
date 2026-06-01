package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// HashIngestKey unit tests
// ---------------------------------------------------------------------------

func TestHashIngestKey(t *testing.T) {
	// Deterministic: same input → same output
	h1 := HashIngestKey("my-secret-key")
	h2 := HashIngestKey("my-secret-key")
	if h1 != h2 {
		t.Fatalf("same input should produce same hash: %q vs %q", h1, h2)
	}
	// Correct length (SHA-256 hex = 64 chars)
	if len(h1) != 64 {
		t.Fatalf("expected 64-char hex hash, got %d chars", len(h1))
	}
	// Different input → different output
	h3 := HashIngestKey("other-key")
	if h1 == h3 {
		t.Fatal("different inputs should produce different hashes")
	}
	// Matches manual SHA-256
	h := sha256.Sum256([]byte("my-secret-key"))
	want := fmt.Sprintf("%x", h)
	if h1 != want {
		t.Fatalf("hash mismatch: got %q, want %q", h1, want)
	}
}

// ---------------------------------------------------------------------------
// mockKeyQuerier implements IngestKeyQuerier for unit tests.
// ---------------------------------------------------------------------------

type mockKeyQuerier struct {
	getKeyFn    func(ctx context.Context, keyHash string) (db.IngestKey, error)
	createKeyFn func(ctx context.Context, arg db.CreateIngestKeyParams) (db.IngestKey, error)
}

func (m *mockKeyQuerier) GetIngestKeyByHash(ctx context.Context, keyHash string) (db.IngestKey, error) {
	if m.getKeyFn != nil {
		return m.getKeyFn(ctx, keyHash)
	}
	return db.IngestKey{}, pgx.ErrNoRows
}

func (m *mockKeyQuerier) CreateIngestKey(ctx context.Context, arg db.CreateIngestKeyParams) (db.IngestKey, error) {
	if m.createKeyFn != nil {
		return m.createKeyFn(ctx, arg)
	}
	return db.IngestKey{}, fmt.Errorf("not implemented")
}

// ---------------------------------------------------------------------------
// ResolveTenantFromKeyDB unit tests
// ---------------------------------------------------------------------------

func TestResolveTenantFromKeyDB_EmptyKey(t *testing.T) {
	q := &mockKeyQuerier{}
	_, err := ResolveTenantFromKeyDB(context.Background(), q, "")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, ErrInvalidIngestKey) {
		t.Fatalf("expected ErrInvalidIngestKey, got %v", err)
	}
}

func TestResolveTenantFromKeyDB_UnknownKey(t *testing.T) {
	q := &mockKeyQuerier{
		getKeyFn: func(ctx context.Context, keyHash string) (db.IngestKey, error) {
			return db.IngestKey{}, pgx.ErrNoRows
		},
	}
	_, err := ResolveTenantFromKeyDB(context.Background(), q, "nonexistent-key")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !errors.Is(err, ErrInvalidIngestKey) {
		t.Fatalf("expected ErrInvalidIngestKey, got %v", err)
	}
}

func TestResolveTenantFromKeyDB_DBFailure(t *testing.T) {
	dbErr := fmt.Errorf("connection refused")
	q := &mockKeyQuerier{
		getKeyFn: func(ctx context.Context, keyHash string) (db.IngestKey, error) {
			return db.IngestKey{}, dbErr
		},
	}
	_, err := ResolveTenantFromKeyDB(context.Background(), q, "some-key")
	if err == nil {
		t.Fatal("expected error for DB failure")
	}
	// DB failure should NOT be classified as ErrInvalidIngestKey.
	if errors.Is(err, ErrInvalidIngestKey) {
		t.Fatalf("DB failure should not be ErrInvalidIngestKey, got %v", err)
	}
	// Should be a wrapped infra error.
	if !strings.Contains(err.Error(), "db lookup for ingest key") {
		t.Fatalf("expected wrapped DB error, got %v", err)
	}
}

func TestResolveTenantFromKeyDB_ValidKey(t *testing.T) {
	q := &mockKeyQuerier{
		getKeyFn: func(ctx context.Context, keyHash string) (db.IngestKey, error) {
			expected := HashIngestKey("valid-key")
			if keyHash != expected {
				t.Fatalf("wrong hash passed to DB: got %q, want %q", keyHash, expected)
			}
			return db.IngestKey{Tenant: "personal", Label: "test"}, nil
		},
	}
	tenant, err := ResolveTenantFromKeyDB(context.Background(), q, "valid-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "personal" {
		t.Fatalf("expected tenant 'personal', got %q", tenant)
	}
}

func TestResolveTenantFromKeyDB_DayjobKey(t *testing.T) {
	q := &mockKeyQuerier{
		getKeyFn: func(ctx context.Context, keyHash string) (db.IngestKey, error) {
			return db.IngestKey{Tenant: "dayjob", Label: "work-laptop"}, nil
		},
	}
	tenant, err := ResolveTenantFromKeyDB(context.Background(), q, "dayjob-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "dayjob" {
		t.Fatalf("expected tenant 'dayjob', got %q", tenant)
	}
}

// ---------------------------------------------------------------------------
// MintIngestKey unit tests
// ---------------------------------------------------------------------------

func TestMintIngestKey_EmptyKey(t *testing.T) {
	q := &mockKeyQuerier{}
	_, _, err := MintIngestKey(context.Background(), q, "", "personal", "label")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestMintIngestKey_EmptyTenant(t *testing.T) {
	q := &mockKeyQuerier{}
	_, _, err := MintIngestKey(context.Background(), q, "key", "", "label")
	if err == nil {
		t.Fatal("expected error for empty tenant")
	}
}

func TestMintIngestKey_Success(t *testing.T) {
	q := &mockKeyQuerier{
		createKeyFn: func(ctx context.Context, arg db.CreateIngestKeyParams) (db.IngestKey, error) {
			expectedHash := HashIngestKey("raw-key")
			if arg.KeyHash != expectedHash {
				t.Fatalf("wrong hash stored: got %q, want %q", arg.KeyHash, expectedHash)
			}
			if arg.Tenant != "personal" {
				t.Fatalf("wrong tenant: got %q", arg.Tenant)
			}
			if arg.Label != "my-laptop" {
				t.Fatalf("wrong label: got %q", arg.Label)
			}
			return db.IngestKey{ID: 1, KeyHash: arg.KeyHash, Tenant: arg.Tenant, Label: arg.Label}, nil
		},
	}
	key, raw, err := MintIngestKey(context.Background(), q, "raw-key", "personal", "my-laptop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw != "raw-key" {
		t.Fatalf("expected raw key returned, got %q", raw)
	}
	if key.Tenant != "personal" {
		t.Fatalf("expected tenant 'personal', got %q", key.Tenant)
	}
	if key.Label != "my-laptop" {
		t.Fatalf("expected label 'my-laptop', got %q", key.Label)
	}
}
