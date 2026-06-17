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

// TestOwnerLRU_BoundedEviction is the negative test for the Hermes security
// blocker (unbounded DEK cache): it proves the cache NEVER grows beyond its
// configured max. Inserting one more entry than the cap evicts the
// least-recently-used entry rather than growing the map.
func TestOwnerLRU_BoundedEviction(t *testing.T) {
	c := newOwnerLRU(3)

	mk := func(b byte) *Cipher {
		key := make([]byte, 32)
		key[0] = b
		cph, err := NewCipher(key)
		require.NoError(t, err)
		return cph
	}

	k1 := [16]byte{1}
	k2 := [16]byte{2}
	k3 := [16]byte{3}
	k4 := [16]byte{4}

	c.store(k1, mk(0x10))
	c.store(k2, mk(0x20))
	c.store(k3, mk(0x30))
	require.Equal(t, 3, c.len(), "at cap, no eviction yet")

	// k4 overflows the cap → LRU (k1) must be evicted, NOT grown to 4.
	c.store(k4, mk(0x40))
	require.Equal(t, 3, c.len(), "must not exceed max")

	_, ok := c.load(k1)
	require.False(t, ok, "k1 (LRU) must have been evicted")

	for _, k := range [][16]byte{k2, k3, k4} {
		got, ok := c.load(k)
		require.True(t, ok, "recent entries must remain cached")
		require.NotNil(t, got)
	}
}

// TestOwnerLRU_RecencyPromotion verifies LRU ordering: loading k1 after k2/k3
// were stored promotes k1, so when the next eviction happens k2 (now LRU) is
// dropped instead of k1.
func TestOwnerLRU_RecencyPromotion(t *testing.T) {
	c := newOwnerLRU(3)

	mk := func(b byte) *Cipher {
		key := make([]byte, 32)
		key[0] = b
		cph, err := NewCipher(key)
		require.NoError(t, err)
		return cph
	}

	k1, k2, k3, k4 := [16]byte{1}, [16]byte{2}, [16]byte{3}, [16]byte{4}
	c.store(k1, mk(1))
	c.store(k2, mk(2))
	c.store(k3, mk(3))

	// Touch k1 so it is no longer the LRU; k2 becomes the LRU.
	_, ok := c.load(k1)
	require.True(t, ok)

	c.store(k4, mk(4)) // evicts LRU == k2

	require.Equal(t, 3, c.len())
	_, ok = c.load(k2)
	require.False(t, ok, "k2 was LRU after k1 promotion and must be evicted")
	_, ok = c.load(k1)
	require.True(t, ok, "k1 was promoted and must survive")
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
