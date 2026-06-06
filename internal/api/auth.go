package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// Identity & ownership middleware (Phase 1 spine, agentos-multiuser-epic-plan.md).
//
// AgentOS trusts identity from EXACTLY ONE source: a trusted, proxy-injected
// header (Tailscale Serve / tsnet for v1). The load-bearing security rule is that
// the API must be unreachable directly so a client cannot forge the header, and
// the proxy strips any client-supplied copy. This middleware does NOT authenticate
// — it maps an already-verified identity string to a User row (lazily creating one
// on first sight) and puts the owner's UUID in the request context, where the data
// layer reads it to scope every query by owner_id.
//
// The store seam (userStore) is a narrow interface satisfied by *db.Queries so the
// middleware is unit-testable against a fake — no Postgres, no generated code needed
// to prove the lookup/create/context behavior.

// TrustedUserHeader is the single header the middleware reads identity from. The
// fronting proxy (Tailscale Serve / oauth2-proxy / Authentik forward-auth) sets it
// to the verified login and MUST strip any client-supplied value. Chosen to match
// the common forward-auth convention so the front door is swappable without a code
// change.
const TrustedUserHeader = "X-Webauth-User"

// ctxKey is an unexported type so context keys can't collide with other packages.
type ctxKey int

const (
	ownerIDKey ctxKey = iota
	ownerLoginKey
)

// userStore is the minimal slice of *db.Queries the identity middleware needs.
// Declaring it here (consumer-side interface) keeps the middleware decoupled from
// the full query surface and trivially fakeable in tests.
type userStore interface {
	GetUserByLogin(ctx context.Context, login string) (db.User, error)
	CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error)
}

// IdentityConfig configures how the middleware resolves identity.
type IdentityConfig struct {
	// DevLogin, when non-empty, is used as the identity for requests that arrive
	// WITHOUT a trusted header. This exists ONLY for local development where no
	// auth proxy fronts the API. In production it MUST be empty: a missing header
	// then yields 401, never a silent default identity. (The plan's "buildable +
	// testable now against a fake header" path.)
	DevLogin string
}

// IdentityConfigFromEnv builds an IdentityConfig from the environment. It FAILS
// CLOSED: by default (AOS_DEV_LOGIN unset) DevLogin is empty, so a request with no
// trusted header is rejected with 401 — never silently resolved to a default user.
// Local development without an auth proxy can OPT IN to a fallback identity by
// setting AOS_DEV_LOGIN=<login> (e.g. "tim"). Reading this one auth-specific var
// beside the middleware keeps any future router.go mount a single line (no config.go
// or NewAPI signature churn).
func IdentityConfigFromEnv() IdentityConfig {
	return IdentityConfig{DevLogin: strings.TrimSpace(os.Getenv("AOS_DEV_LOGIN"))}
}

// IdentityMiddleware returns chi-compatible middleware that resolves the trusted
// identity header to a User (lazily creating one), and injects the owner UUID into
// the request context. Requests with no resolvable identity get 401.
//
// Lazy-create rationale: a trusted header means the proxy already verified the human;
// the first time we see that login we materialize their User row so ownership works
// immediately, no separate signup step. Login is the unique natural key, so concurrent
// first-requests race to the same row — a create that loses the race (duplicate-key)
// falls back to a re-fetch rather than erroring.
func IdentityMiddleware(store userStore, cfg IdentityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			login := strings.TrimSpace(r.Header.Get(TrustedUserHeader))
			if login == "" {
				login = strings.TrimSpace(cfg.DevLogin)
			}
			if login == "" {
				http.Error(w, "unauthorized: no trusted identity", http.StatusUnauthorized)
				return
			}

			user, err := resolveUser(r.Context(), store, login)
			if err != nil {
				http.Error(w, "failed to resolve identity", http.StatusInternalServerError)
				return
			}
			if !user.IsActive {
				http.Error(w, "forbidden: account is not active", http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), ownerIDKey, user.ID)
			ctx = context.WithValue(ctx, ownerLoginKey, user.Login)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveUser looks up the user by login, lazily creating one on first sight. On a
// create that loses a concurrent race (unique-violation on login) it re-fetches, so
// two simultaneous first requests both resolve to the one canonical row.
//
// A lookup error that is NOT a clean "no rows" miss (e.g. a transient connection
// failure) is returned as-is — we must NOT treat it as "user absent" and create a
// row, which would both mask the real error and risk materializing an unintended user.
func resolveUser(ctx context.Context, store userStore, login string) (db.User, error) {
	user, err := store.GetUserByLogin(ctx, login)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		// Real read error (not a miss): surface it; caller maps to 500.
		return db.User{}, err
	}

	// Genuine miss: attempt to create. Display name defaults to the login; it can be
	// edited later via a profile surface.
	created, createErr := store.CreateUser(ctx, db.CreateUserParams{
		Login:       login,
		DisplayName: login,
	})
	if createErr == nil {
		return created, nil
	}

	// Lost the create race (another request created the row first): re-fetch.
	if isUniqueViolation(createErr) {
		return store.GetUserByLogin(ctx, login)
	}
	return db.User{}, createErr
}

// isUniqueViolation reports whether err is a Postgres unique-constraint violation
// (SQLSTATE 23505). Uses the typed pgconn.PgError code rather than string matching
// so it can't be fooled by an unrelated message that happens to contain the digits.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// OwnerIDFromContext returns the authenticated owner's UUID and true when the
// identity middleware has run for the request. Downstream repository/query code
// uses this to scope reads/writes by owner_id. Returns ok=false when absent so
// callers can fail closed rather than leaking cross-owner data.
func OwnerIDFromContext(ctx context.Context) (pgtype.UUID, bool) {
	id, ok := ctx.Value(ownerIDKey).(pgtype.UUID)
	return id, ok
}

// OwnerLoginFromContext returns the authenticated owner's login and true when the
// identity middleware has run for the request.
func OwnerLoginFromContext(ctx context.Context) (string, bool) {
	login, ok := ctx.Value(ownerLoginKey).(string)
	return login, ok
}
