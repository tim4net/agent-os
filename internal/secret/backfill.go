package secret

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tim4net/agent-os/internal/db"
)

func RunBackfill(ctx context.Context, env *EnvelopeCipher, queries *db.Queries, log *slog.Logger) error {
	if !env.Enabled() {
		return nil
	}

	legacy, err := queries.ListLegacyResources(ctx)
	if err != nil {
		return fmt.Errorf("list legacy resources: %w", err)
	}

	if len(legacy) == 0 {
		return nil
	}

	log.Info("running secret encryption backfill", "count", len(legacy))

	var migrated int
	for _, res := range legacy {
		if !res.OwnerID.Valid {
			continue
		}

		ownerID := res.OwnerID.Bytes

		err := env.EnsureOwnerKey(ctx, ownerID)
		if err != nil {
			return fmt.Errorf("ensure owner key for %s: %w", res.ID, err)
		}

		plaintext, err := env.kek.Decrypt(res.EncValue)
		if err != nil {
			return fmt.Errorf("decrypt legacy secret for %s: %w", res.ID, err)
		}

		newCiphertext, err := env.EncryptForOwner(ctx, ownerID, plaintext)
		if err != nil {
			return fmt.Errorf("re-encrypt secret for %s: %w", res.ID, err)
		}

		err = queries.UpdateResourceEncryption(ctx, db.UpdateResourceEncryptionParams{
			ID:            res.ID,
			EncValue:      newCiphertext,
			EncKeyVersion: 1,
		})
		if err != nil {
			return fmt.Errorf("update resource encryption for %s: %w", res.ID, err)
		}
		migrated++
	}

	log.Info("completed secret encryption backfill", "migrated", migrated)
	return nil
}
