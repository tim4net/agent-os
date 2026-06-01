package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/tim4net/agent-os/internal/db"
)

// IngestKeyQuerier is the subset of db.Querier needed for ingest-key operations.
// Using a narrow interface keeps the resolver testable without mocking the full
// Querier surface.
type IngestKeyQuerier interface {
	GetIngestKeyByHash(ctx context.Context, keyHash string) (db.IngestKey, error)
	CreateIngestKey(ctx context.Context, arg db.CreateIngestKeyParams) (db.IngestKey, error)
}

// Compile-time check: *db.Queries satisfies IngestKeyQuerier.
var _ IngestKeyQuerier = (*db.Queries)(nil)

// HashIngestKey returns the SHA-256 hex digest of a raw ingest key.
// Raw keys are never stored or logged — only their hashes.
func HashIngestKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", h)
}

// ErrInvalidIngestKey is a sentinel error returned by ResolveTenantFromKeyDB
// when the ingest key is empty, unknown, or revoked. The handler uses this to
// classify the response as 403 (auth rejection) vs 500 (infra failure).
var ErrInvalidIngestKey = errors.New("invalid ingest key")

// ResolveTenantFromKeyDB resolves a tenant from an ingest key using the
// durable ingest_keys table. It hashes the raw key and looks up the
// (non-revoked) key in the database. Returns an error if the key is empty,
// unknown, or revoked.
//
// This replaces the old env-backed ResolveTenantFromKey placeholder.
func ResolveTenantFromKeyDB(ctx context.Context, querier IngestKeyQuerier, rawKey string) (string, error) {
	if rawKey == "" {
		return "", fmt.Errorf("%w: missing ingest key", ErrInvalidIngestKey)
	}
	keyHash := HashIngestKey(rawKey)
	row, err := querier.GetIngestKeyByHash(ctx, keyHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Default().Warn("ingest key not found (unknown or revoked)", "key_hash_prefix", keyHash[:8])
			return "", ErrInvalidIngestKey
		}
		return "", fmt.Errorf("db lookup for ingest key: %w", err)
	}
	return row.Tenant, nil
}

// MintIngestKey creates a new ingest key in the database. It hashes the raw
// key, inserts the hashed record, and returns the created row. The raw key
// is returned to the caller exactly once (this function) — it is never stored.
func MintIngestKey(ctx context.Context, querier IngestKeyQuerier, rawKey, tenant, label string) (db.IngestKey, string, error) {
	if rawKey == "" {
		return db.IngestKey{}, "", fmt.Errorf("raw key must not be empty")
	}
	if tenant == "" {
		return db.IngestKey{}, "", fmt.Errorf("tenant must not be empty")
	}
	keyHash := HashIngestKey(rawKey)
	created, err := querier.CreateIngestKey(ctx, db.CreateIngestKeyParams{
		KeyHash: keyHash,
		Tenant:  tenant,
		Label:   label,
	})
	if err != nil {
		return db.IngestKey{}, "", fmt.Errorf("create ingest key: %w", err)
	}
	return created, rawKey, nil
}
