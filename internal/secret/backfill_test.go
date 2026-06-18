package secret

import (
	"context"
	"log/slog"
	"testing"
	"time"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"github.com/tim4net/agent-os/internal/db"
)

func TestBackfill_Idempotent(t *testing.T) {
	queries, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	masterKey := make([]byte, 32)
	kek, err := NewCipher(masterKey)
	require.NoError(t, err)

	plaintext := "legacy secret"
	ct, err := kek.Encrypt(plaintext)
	require.NoError(t, err)

	defaultOwner := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

	// Create legacy resource (enc_key_version defaults to 0 based on DB migrations, but we added it to CreateResourceParams)
	res, err := queries.CreateResource(ctx, db.CreateResourceParams{
		Slug:          fmt.Sprintf("test-legacy-bf-%d", time.Now().UnixNano()),
		Kind:          "credential",
		Label:         "Legacy",
		Provider:      "test",
		IsSecret:      true,
		EncValue:      ct,
		Config:        []byte("{}"),
		Last4:         Last4(plaintext),
		Status:        "active",
		EncKeyVersion: 0,
		OwnerID:       pgtype.UUID{Bytes: defaultOwner, Valid: true},
	})
	require.NoError(t, err)

	env := NewEnvelopeCipher(kek, queries)

	// First run
	err = RunBackfill(ctx, env, queries, slog.Default())
	require.NoError(t, err)

	// Verify migrated
	migratedRes, err := queries.GetResource(ctx, db.GetResourceParams{ID: res.ID, OwnerID: pgtype.UUID{Bytes: defaultOwner, Valid: true}})
	require.NoError(t, err)
	require.Equal(t, int32(1), migratedRes.EncKeyVersion)

	// Verify we can decrypt with owner key
	dec, err := env.DecryptForOwner(ctx, defaultOwner, migratedRes.EncValue)
	require.NoError(t, err)
	require.Equal(t, plaintext, dec)

	// Second run should be idempotent
	err = RunBackfill(ctx, env, queries, slog.Default())
	require.NoError(t, err)
}
