package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// fakeUserStore is an in-memory userStore for driving the identity middleware
// without Postgres. It records calls so tests can assert lazy-create happened (or
// did NOT happen) and can be programmed to fail or simulate a create race.
type fakeUserStore struct {
	mu sync.Mutex

	byLogin map[string]db.User

	// getErr, when set, is returned by GetUserByLogin instead of a not-found miss
	// for the FIRST lookup (used to simulate transient read errors).
	getErr error
	// createErr, when set, is returned by CreateUser (used to simulate a lost race
	// via a duplicate-key error, or a hard failure).
	createErr error
	// raceInsert, when non-nil, is inserted into byLogin the first time CreateUser
	// is called BEFORE createErr is returned — simulating another request winning
	// the create race so the re-fetch path finds the row.
	raceInsert *db.User

	getCalls    int
	createCalls int
}

func newFakeStore() *fakeUserStore {
	return &fakeUserStore{byLogin: map[string]db.User{}}
}

func (f *fakeUserStore) GetUserByLogin(_ context.Context, login string) (db.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil && f.getCalls == 1 {
		return db.User{}, f.getErr
	}
	u, ok := f.byLogin[login]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeUserStore) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.raceInsert != nil {
		f.byLogin[arg.Login] = *f.raceInsert
	}
	if f.createErr != nil {
		return db.User{}, f.createErr
	}
	u := db.User{
		ID:          newUUID(),
		Login:       arg.Login,
		DisplayName: arg.DisplayName,
		IsActive:    true,
	}
	f.byLogin[arg.Login] = u
	return u, nil
}

// newUUID returns a valid, non-zero pgtype.UUID for fake rows.
func newUUID() pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan("11111111-1111-1111-1111-111111111111")
	return u
}

func seedUser(f *fakeUserStore, login string, active bool) db.User {
	var id pgtype.UUID
	_ = id.Scan("22222222-2222-2222-2222-222222222222")
	u := db.User{ID: id, Login: login, DisplayName: login, IsActive: active}
	f.byLogin[login] = u
	return u
}

// captureHandler is the downstream handler; it records the owner id/login the
// middleware injected so tests can assert the context was populated correctly.
type captureHandler struct {
	called     bool
	gotOwnerID pgtype.UUID
	gotOwnerOK bool
	gotLogin   string
	gotLoginOK bool
}

func (h *captureHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	h.called = true
	h.gotOwnerID, h.gotOwnerOK = OwnerIDFromContext(r.Context())
	h.gotLogin, h.gotLoginOK = OwnerLoginFromContext(r.Context())
}

func runMiddleware(store userStore, cfg IdentityConfig, mutate func(*http.Request)) (*httptest.ResponseRecorder, *captureHandler) {
	next := &captureHandler{}
	mw := IdentityMiddleware(store, cfg)(next)
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	return rec, next
}

// AC1: a trusted header for an EXISTING user resolves to that user, injects the
// owner id + login, and calls the next handler. No create.
func TestIdentityMiddleware_ExistingUser(t *testing.T) {
	store := newFakeStore()
	want := seedUser(store, "tim", true)

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "tim")
	})

	if !next.called {
		t.Fatalf("next handler was not called; status=%d body=%q", rec.Code, rec.Body.String())
	}
	if !next.gotOwnerOK {
		t.Fatalf("owner id not present in context")
	}
	if next.gotOwnerID != want.ID {
		t.Errorf("owner id = %v, want %v", next.gotOwnerID, want.ID)
	}
	if next.gotLogin != "tim" {
		t.Errorf("owner login = %q, want %q", next.gotLogin, "tim")
	}
	if store.createCalls != 0 {
		t.Errorf("CreateUser called %d times for an existing user; want 0", store.createCalls)
	}
}

// AC2: a trusted header for an UNKNOWN login lazily creates the user and resolves.
func TestIdentityMiddleware_LazyCreate(t *testing.T) {
	store := newFakeStore()

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "newhuman")
	})

	if !next.called {
		t.Fatalf("next handler not called; status=%d", rec.Code)
	}
	if store.createCalls != 1 {
		t.Errorf("CreateUser called %d times; want 1 (lazy create)", store.createCalls)
	}
	if !next.gotOwnerOK {
		t.Errorf("owner id not injected after lazy create")
	}
	if next.gotLogin != "newhuman" {
		t.Errorf("owner login = %q, want %q", next.gotLogin, "newhuman")
	}
}

// AC3: NO trusted header and NO dev login → 401, next handler NOT called.
func TestIdentityMiddleware_NoIdentity_401(t *testing.T) {
	store := newFakeStore()

	rec, next := runMiddleware(store, IdentityConfig{}, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Errorf("next handler was called despite missing identity")
	}
}

// AC4: NO trusted header but a configured DevLogin → resolves as that dev user
// (local-dev path). Proves the dev fallback works only when explicitly configured.
func TestIdentityMiddleware_DevLoginFallback(t *testing.T) {
	store := newFakeStore()
	seedUser(store, "devuser", true)

	rec, next := runMiddleware(store, IdentityConfig{DevLogin: "devuser"}, nil)

	if !next.called {
		t.Fatalf("next handler not called with dev login; status=%d", rec.Code)
	}
	if next.gotLogin != "devuser" {
		t.Errorf("owner login = %q, want %q", next.gotLogin, "devuser")
	}
}

// AC5: a present trusted header WINS over a configured dev login — i.e. the proxy's
// verified identity is authoritative, the dev default is only a no-header fallback.
// This guards the proxy-strip security model: real identity is never shadowed by the
// dev default.
func TestIdentityMiddleware_HeaderWinsOverDevLogin(t *testing.T) {
	store := newFakeStore()
	seedUser(store, "realuser", true)
	seedUser(store, "devuser", true)

	_, next := runMiddleware(store, IdentityConfig{DevLogin: "devuser"}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "realuser")
	})

	if next.gotLogin != "realuser" {
		t.Errorf("owner login = %q, want %q (header must win over dev login)", next.gotLogin, "realuser")
	}
}

// AC6: an INACTIVE user is rejected with 403, next handler NOT called.
func TestIdentityMiddleware_InactiveUser_403(t *testing.T) {
	store := newFakeStore()
	seedUser(store, "suspended", false)

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "suspended")
	})

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if next.called {
		t.Errorf("next handler called for an inactive user")
	}
}

// AC7: a hard create failure (not a race) surfaces as 500, next NOT called.
func TestIdentityMiddleware_CreateFailure_500(t *testing.T) {
	store := newFakeStore()
	store.createErr = errors.New("connection refused")

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "whoever")
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if next.called {
		t.Errorf("next handler called despite resolve failure")
	}
}

// AC9: a TRANSIENT read error from GetUserByLogin (NOT a clean miss) must surface as
// 500 and must NOT trigger a lazy create — otherwise a flaky DB read could mask the
// error and/or materialize an unintended user. (Review finding #1.)
func TestIdentityMiddleware_TransientReadError_500_NoCreate(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("connection reset by peer")

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "tim")
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d on transient read error", rec.Code, http.StatusInternalServerError)
	}
	if next.called {
		t.Errorf("next handler called despite transient read error")
	}
	if store.createCalls != 0 {
		t.Errorf("CreateUser called %d times after a transient read error; want 0 (must not create)", store.createCalls)
	}
}

// A create error that is NOT a unique-violation (e.g. a check-constraint or hard
// failure) is surfaced as 500, not mistaken for a lost race. Guards the typed
// isUniqueViolation against false positives. (Review finding #2.)
//
// raceInsert is set so that IF isUniqueViolation wrongly classified this 23514 as a
// race, the re-fetch would FIND a row and resolve 200 — making the misclassification
// observable. With correct classification the re-fetch never happens and we get 500.
func TestIdentityMiddleware_NonUniqueCreateError_500(t *testing.T) {
	store := newFakeStore()
	var id pgtype.UUID
	_ = id.Scan("44444444-4444-4444-4444-444444444444")
	decoy := db.User{ID: id, Login: "whoever", DisplayName: "whoever", IsActive: true}
	store.raceInsert = &decoy
	store.createErr = &pgconn.PgError{Code: "23514", Message: "new row violates check constraint"}

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "whoever")
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d on non-unique create error (must NOT be treated as a race)", rec.Code, http.StatusInternalServerError)
	}
	if next.called {
		t.Errorf("next handler called despite non-unique create error")
	}
}

// AC8: losing the create race (duplicate-key on create) re-fetches the row another
// request created, and still resolves successfully.
func TestIdentityMiddleware_CreateRace_RefetchSucceeds(t *testing.T) {
	store := newFakeStore()
	var raced db.User
	var id pgtype.UUID
	_ = id.Scan("33333333-3333-3333-3333-333333333333")
	raced = db.User{ID: id, Login: "racer", DisplayName: "racer", IsActive: true}
	store.raceInsert = &raced
	store.createErr = &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint \"users_login_key\""}

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "racer")
	})

	if !next.called {
		t.Fatalf("next handler not called after race re-fetch; status=%d", rec.Code)
	}
	if next.gotOwnerID != id {
		t.Errorf("owner id = %v, want raced row %v", next.gotOwnerID, id)
	}
}

// Guard: a whitespace-only header is treated as no identity (trim), → 401. Prevents
// a proxy misconfig sending "  " from creating a junk empty-login user.
func TestIdentityMiddleware_WhitespaceHeader_401(t *testing.T) {
	store := newFakeStore()

	rec, next := runMiddleware(store, IdentityConfig{}, func(r *http.Request) {
		r.Header.Set(TrustedUserHeader, "   ")
	})

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d for whitespace header", rec.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Errorf("next handler called for whitespace identity")
	}
	if store.createCalls != 0 {
		t.Errorf("CreateUser called %d times for whitespace header; want 0", store.createCalls)
	}
}

// Context helpers return ok=false when the middleware has NOT run, so callers fail
// closed rather than treating a zero UUID as a real owner.
func TestOwnerContextHelpers_AbsentWhenNoMiddleware(t *testing.T) {
	if _, ok := OwnerIDFromContext(context.Background()); ok {
		t.Errorf("OwnerIDFromContext ok=true on a bare context; want false")
	}
	if _, ok := OwnerLoginFromContext(context.Background()); ok {
		t.Errorf("OwnerLoginFromContext ok=true on a bare context; want false")
	}
}

// IdentityConfigFromEnv FAILS CLOSED: when AOS_DEV_LOGIN is UNSET, DevLogin is empty
// so a no-header request is rejected (401), never silently resolved to a default user.
func TestIdentityConfigFromEnv_UnsetFailsClosed(t *testing.T) {
	t.Setenv("AOS_DEV_LOGIN", "x")
	os.Unsetenv("AOS_DEV_LOGIN")
	if got := IdentityConfigFromEnv(); got.DevLogin != "" {
		t.Errorf("DevLogin = %q, want \"\" (fail closed) when AOS_DEV_LOGIN unset", got.DevLogin)
	}
}

// Explicitly opting into a dev fallback identity is honored (local-dev convenience).
func TestIdentityConfigFromEnv_OptInDevLogin(t *testing.T) {
	t.Setenv("AOS_DEV_LOGIN", "tim")
	if got := IdentityConfigFromEnv(); got.DevLogin != "tim" {
		t.Errorf("DevLogin = %q, want %q when explicitly set", got.DevLogin, "tim")
	}
}

// A whitespace-only AOS_DEV_LOGIN is trimmed to empty → still fails closed.
func TestIdentityConfigFromEnv_WhitespaceFailsClosed(t *testing.T) {
	t.Setenv("AOS_DEV_LOGIN", "   ")
	if got := IdentityConfigFromEnv(); got.DevLogin != "" {
		t.Errorf("DevLogin = %q, want \"\" for whitespace-only AOS_DEV_LOGIN", got.DevLogin)
	}
}
