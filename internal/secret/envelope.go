package secret

import (
	"container/list"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// defaultOwnerCacheMax bounds the number of per-owner DEK ciphers held in
// memory at once. Each entry is a 16-byte key + an AES-256 *Cipher (~negligible),
// so this is a safety ceiling against memory-exhaustion via an unbounded set of
// distinct owner IDs rather than a tight resource limit. A miss after eviction
// simply re-loads the wrapped DEK from the database and re-caches it.
const defaultOwnerCacheMax = 1024

// cacheEntry is one element of the LRU list.
type cacheEntry struct {
	key    [16]byte
	cipher *Cipher
}

// ownerLRU is a bounded, mutex-protected LRU cache of owner -> DEK cipher.
//
// It replaces the previous unbounded sync.Map, which grew without limit as new
// owner IDs were seen — a memory-exhaustion vector flagged in the Hermes
// security review. Once max entries is reached the least-recently-used entry is
// evicted; a subsequent miss transparently re-loads the DEK from the database.
type ownerLRU struct {
	mu      sync.Mutex
	max     int
	ll      *list.List
	entries map[[16]byte]*list.Element
}

func newOwnerLRU(max int) *ownerLRU {
	if max < 1 {
		max = 1
	}
	return &ownerLRU{
		max:     max,
		ll:      list.New(),
		entries: make(map[[16]byte]*list.Element, max),
	}
}

// load returns the cached cipher for key, marking it most-recently-used. The
// second return is false on a miss.
func (c *ownerLRU) load(key [16]byte) (*Cipher, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*cacheEntry).cipher, true
}

// store inserts or refreshes an entry, evicting the LRU entry if at capacity.
func (c *ownerLRU) store(key [16]byte, cipher *Cipher) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		el.Value.(*cacheEntry).cipher = cipher
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&cacheEntry{key: key, cipher: cipher})
	c.entries[key] = el
	if c.ll.Len() > c.max {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			delete(c.entries, oldest.Value.(*cacheEntry).key)
		}
	}
}

func (c *ownerLRU) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

type EnvelopeCipher struct {
	kek     *Cipher
	queries *db.Queries
	cache   *ownerLRU
}

func NewEnvelopeCipher(kek *Cipher, queries *db.Queries) *EnvelopeCipher {
	return &EnvelopeCipher{
		kek:     kek,
		queries: queries,
		cache:   newOwnerLRU(defaultOwnerCacheMax),
	}
}

func (e *EnvelopeCipher) Enabled() bool {
	return e != nil && e.kek.Enabled()
}

func (e *EnvelopeCipher) EnsureOwnerKey(ctx context.Context, ownerID [16]byte) error {
	if !e.Enabled() {
		return ErrNoKey
	}

	if _, ok := e.cache.load(ownerID); ok {
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
		e.cache.store(ownerID, cipher)
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
		e.cache.store(ownerID, cipher)
		return nil
	}

	cipher, err := NewCipher(dek)
	if err != nil {
		return err
	}
	e.cache.store(ownerID, cipher)
	return nil
}

func (e *EnvelopeCipher) getOwnerCipher(ctx context.Context, ownerID [16]byte) (*Cipher, error) {
	if !e.Enabled() {
		return nil, ErrNoKey
	}

	if c, ok := e.cache.load(ownerID); ok {
		return c, nil
	}

	err := e.EnsureOwnerKey(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	if c, ok := e.cache.load(ownerID); ok {
		return c, nil
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
