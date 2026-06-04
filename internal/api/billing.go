package api

import "strings"

// BillingMode classifies how an agent's token usage is billed. This is the
// foundation of provider-aware spend: token/turn usage is ALWAYS meaningful,
// but a dollar figure is only meaningful for metered (pay-per-token) accounts.
// Subscription accounts pay a flat fee, so a per-session dollar cost is not a
// real bill and must not be presented as one.
type BillingMode string

const (
	// BillingSubscription = flat-rate plan (Claude Pro/Max, Gemini plan, etc.).
	// Dollar cost is NOT meaningful; surface token/turn usage instead.
	BillingSubscription BillingMode = "subscription"
	// BillingMetered = pay-per-token API billing. Dollar cost is real.
	BillingMetered BillingMode = "metered"
	// BillingUnknown = provider not recognized; treat cost as not-applicable.
	BillingUnknown BillingMode = "unknown"
)

// providerBillingMode is the centralized provider→billing-mode map (Option A).
//
// Tim owns this table. Agents do NOT self-report billing mode today; the system
// is "aware of its providers" through this map. Option B (agents self-reporting
// an authoritative billing_mode in telemetry, with real per-token cost models)
// is a planned future override — wire it in by having ResolveBillingMode prefer
// an explicit telemetry value when present. Until then this default keeps the
// UI honest without blocking on per-token accuracy.
var providerBillingMode = map[string]BillingMode{
	"anthropic": BillingSubscription,
	"google":    BillingSubscription,
	"openai":    BillingMetered,
}

// harnessProvider maps a known harness to its default provider. Used when the
// telemetry model string is absent. "generic" is intentionally unmapped so it
// resolves to the unknown provider rather than guessing.
var harnessProvider = map[string]string{
	"claude":      "anthropic",
	"hermes":      "anthropic",
	"antigravity": "google",
	"codex":       "openai",
}

// ResolveProvider derives the provider from the telemetry model string (most
// specific signal) and falls back to the harness default. Returns "" (unknown)
// when neither resolves. Model prefixes are matched case-insensitively.
func ResolveProvider(harness, model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gemini"):
		return "google"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"),
		strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return "openai"
	}
	if p, ok := harnessProvider[strings.ToLower(strings.TrimSpace(harness))]; ok {
		return p
	}
	return ""
}

// BillingModeFor returns the billing mode for a resolved provider (Option A).
func BillingModeFor(provider string) BillingMode {
	if mode, ok := providerBillingMode[provider]; ok {
		return mode
	}
	return BillingUnknown
}

// ResolveBillingMode is the convenience entry point: harness + optional model →
// billing mode. When Option B lands, this is where an explicit, authoritative
// telemetry billing_mode should take precedence over the centralized map.
func ResolveBillingMode(harness, model string) BillingMode {
	return BillingModeFor(ResolveProvider(harness, model))
}
