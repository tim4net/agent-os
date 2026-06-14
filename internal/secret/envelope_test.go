package secret

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/tim4net/agent-os/internal/db"
)

func setupTestDB(t *testing.T) (*db.Queries, func()) {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	// CI pre-migrates the database via raw psql before running tests, so we
	// must NOT call MigrateUp here — it would see an empty schema_migrations
	// table, re-apply migration 1, collide with existing tables, and leave a
	// dirty-migration error.
	//
	// Clear per-owner encryption state so tests with different KEKs don't
	// trip over stale wrapped DEKs left by a prior test against this shared DB.
	_, _ = pool.Exec(ctx, "TRUNCATE user_keys, resources CASCADE")
	return db.New(pool), func() {
		pool.Close()
	}
}

func TestEnvelope_PerUserIsolation(t *testing.T) {
	queries, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	uA, err := queries.CreateUser(ctx, db.CreateUserParams{
		Login: fmt.Sprintf("usera_env_iso_%d", time.Now().UnixNano()),
	})
	require.NoError(t, err)
	userA := uA.ID.Bytes

	uB, err := queries.CreateUser(ctx, db.CreateUserParams{
		Login: fmt.Sprintf("userb_env_iso_%d", time.Now().UnixNano()),
	})
	require.NoError(t, err)
	userB := uB.ID.Bytes

	masterKey := make([]byte, 32)
	kek, err := NewCipher(masterKey)
	require.NoError(t, err)

	env := NewEnvelopeCipher(kek, queries)

	plaintext := "super secret"
	ctA, err := env.EncryptForOwner(ctx, userA, plaintext)
	require.NoError(t, err)

	decA, err := env.DecryptForOwner(ctx, userA, ctA)
	require.NoError(t, err)
	require.Equal(t, plaintext, decA)

	_, err = env.DecryptForOwner(ctx, userB, ctA)
	require.Error(t, err)
}

func TestEnvelope_NoMasterKey(t *testing.T) {
	queries, cleanup := setupTestDB(t)
	defer cleanup()

	env := NewEnvelopeCipher(nil, queries)
	require.False(t, env.Enabled())

	ctx := context.Background()
	userA := [16]byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	_, err := env.EncryptForOwner(ctx, userA, "test")
	require.ErrorIs(t, err, ErrNoKey)

	_, err = env.DecryptForOwner(ctx, userA, []byte("test"))
	require.ErrorIs(t, err, ErrNoKey)
}
