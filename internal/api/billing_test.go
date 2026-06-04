package api

import "testing"

func TestResolveProvider(t *testing.T) {
	cases := []struct {
		harness, model, want string
	}{
		// Model string wins (most specific).
		{"generic", "claude-opus-4-8", "anthropic"},
		{"generic", "Gemini-3.5-Flash", "google"},
		{"generic", "gpt-5.5", "openai"},
		{"generic", "o3-mini", "openai"},
		// Harness fallback when no model.
		{"claude", "", "anthropic"},
		{"hermes", "", "anthropic"},
		{"antigravity", "", "google"},
		{"codex", "", "openai"},
		// Unknown harness, no model → unknown provider.
		{"generic", "", ""},
		{"weird", "", ""},
	}
	for _, c := range cases {
		if got := ResolveProvider(c.harness, c.model); got != c.want {
			t.Errorf("ResolveProvider(%q,%q)=%q, want %q", c.harness, c.model, got, c.want)
		}
	}
}

func TestResolveBillingMode(t *testing.T) {
	cases := []struct {
		harness, model string
		want           BillingMode
	}{
		{"claude", "", BillingSubscription},
		{"hermes", "", BillingSubscription},
		{"antigravity", "", BillingSubscription},
		{"codex", "", BillingMetered},
		{"generic", "gpt-5.5", BillingMetered},
		{"generic", "claude-3", BillingSubscription},
		{"generic", "", BillingUnknown},
	}
	for _, c := range cases {
		if got := ResolveBillingMode(c.harness, c.model); got != c.want {
			t.Errorf("ResolveBillingMode(%q,%q)=%q, want %q", c.harness, c.model, got, c.want)
		}
	}
}
