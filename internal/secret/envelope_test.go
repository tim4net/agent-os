package secret

import (
	"context"
	"log/slog"
	"testing"
	"time"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/tim4net/agent-os/internal/db"
)

func setupTestDB(t *testing.T) (*db.Queries, func()) {
	ctx := context.Background()
	connStr := "postgres://aos_test:aos_test_pw@localhost:55434/aos_test?sslmode=disable"
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)

	_, err = db.MigrateUpWithLogger(ctx, pool, slog.Default())
	require.NoError(t, err)

	queries := db.New(pool)
	
	return queries, func() {
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
