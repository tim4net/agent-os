package api

import (
	"os"
	"testing"
)

// TestMain sets AOS_DEV_LOGIN=tim for the entire api test binary so the
// IdentityMiddleware (now mounted in Router()) resolves every request that
// flows through the full router to the seed owner-0 user (login "tim", UUID
// 00000000-0000-0000-0000-000000000001 from migration 024). All test seed
// data inherits owner-0 via the DEFAULT added in migration 025, so handlers
// scope queries correctly.
//
// Tests that bypass Router() (calling handler methods directly with
// withTestOwner()) are unaffected — the middleware never runs. Tests that
// assert fail-closed identity behavior (TestIdentityConfigFromEnv_*,
// TestIdentityMiddleware_NoIdentity_401) override locally with t.Setenv or
// pass an explicit IdentityConfig, so they are also unaffected.
//
// Without this, every integration test that calls a.Router().ServeHTTP()
// returns 401 "no trusted identity" because the middleware has no header and
// no DevLogin to fall back on.
func TestMain(m *testing.M) {
	os.Setenv("AOS_DEV_LOGIN", "tim")
	os.Exit(m.Run())
}
