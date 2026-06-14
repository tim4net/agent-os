package secret

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

type EnvelopeCipher struct {
	kek     *Cipher
	queries *db.Queries
	cache   sync.Map // map[[16]byte]*Cipher
}

func NewEnvelopeCipher(kek *Cipher, queries *db.Queries) *EnvelopeCipher {
	return &EnvelopeCipher{
		kek:     kek,
		queries: queries,
	}
}

func (e *EnvelopeCipher) Enabled() bool {
	return e != nil && e.kek.Enabled()
}

func (e *EnvelopeCipher) EnsureOwnerKey(ctx context.Context, ownerID [16]byte) error {
	if !e.Enabled() {
		return ErrNoKey
	}

	if _, ok := e.cache.Load(ownerID); ok {
		return nil
	}

	uuid := pgtype.UUID{Bytes: ownerID, Valid: true}
	record, err := e.queries.GetUserKey(ctx, uuid)
	if err == nil {
		rawDEK, err := e.kek.Decrypt(record.WrappedDek)
		if err != nil {
			return fmt.Errorf("secret: unwrap dek: %w", err)
		}

		cipher, err := NewCipher([]byte(rawDEK))
		if err != nil {
			return err
		}
		e.cache.Store(ownerID, cipher)
		return nil
	}

	// Key not found, create new
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return fmt.Errorf("secret: generate dek: %w", err)
	}

	wrappedDEK, err := e.kek.Encrypt(string(dek))
	if err != nil {
		return fmt.Errorf("secret: wrap dek: %w", err)
	}

	_, err = e.queries.CreateUserKey(ctx, db.CreateUserKeyParams{
		UserID:     uuid,
		WrappedDek: wrappedDEK,
	})

	if err != nil {
		// Possibly another concurrent insertion happened and ON CONFLICT returned ErrNoRows.
		// Try fetching again
		record, getErr := e.queries.GetUserKey(ctx, uuid)
		if getErr != nil {
			return fmt.Errorf("secret: persist dek failed (%v), and get failed: %w", err, getErr)
		}
		rawDEK, err := e.kek.Decrypt(record.WrappedDek)
		if err != nil {
			return fmt.Errorf("secret: unwrap dek: %w", err)
		}
		cipher, err := NewCipher([]byte(rawDEK))
		if err != nil {
			return err
		}
		e.cache.Store(ownerID, cipher)
		return nil
	}

	cipher, err := NewCipher(dek)
	if err != nil {
		return err
	}
	e.cache.Store(ownerID, cipher)
	return nil
}

func (e *EnvelopeCipher) getOwnerCipher(ctx context.Context, ownerID [16]byte) (*Cipher, error) {
	if !e.Enabled() {
		return nil, ErrNoKey
	}

	if val, ok := e.cache.Load(ownerID); ok {
		return val.(*Cipher), nil
	}

	err := e.EnsureOwnerKey(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	if val, ok := e.cache.Load(ownerID); ok {
		return val.(*Cipher), nil
	}
	return nil, fmt.Errorf("secret: key not found after ensure")
}

func (e *EnvelopeCipher) EncryptForOwner(ctx context.Context, ownerID [16]byte, plaintext string) ([]byte, error) {
	cipher, err := e.getOwnerCipher(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	return cipher.Encrypt(plaintext)
}

func (e *EnvelopeCipher) DecryptForOwner(ctx context.Context, ownerID [16]byte, ciphertext []byte) (string, error) {
	cipher, err := e.getOwnerCipher(ctx, ownerID)
	if err != nil {
		return "", err
	}

	return cipher.Decrypt(ciphertext)
}
