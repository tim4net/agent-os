package api

import (
	"strings"
	"testing"
)

func TestModeSystemPrompt_Perplexity(t *testing.T) {
	got := modeSystemPrompt(ModePerplexity)
	if got == "" {
		t.Fatal("perplexity mode must return a non-empty system prompt")
	}
	// The prompt must instruct the agent to search the web and cite sources —
	// the two acceptance-criteria behaviours for #137.
	for _, want := range []string{"web-search", "cit", "Sources"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("perplexity prompt missing %q (want instruction to reference it)", want)
		}
	}
}

func TestModeSystemPrompt_SearchAlias(t *testing.T) {
	// "search" is an accepted alias for the perplexity mode.
	if modeSystemPrompt("search") == "" {
		t.Fatal(`"search" must map to the perplexity prompt`)
	}
}

func TestModeSystemPrompt_CaseInsensitive(t *testing.T) {
	if modeSystemPrompt("PERPLEXITY") == "" {
		t.Fatal("mode lookup must be case-insensitive")
	}
}

func TestModeSystemPrompt_DefaultIsEmpty(t *testing.T) {
	if modeSystemPrompt(ModeDefault) != "" {
		t.Fatal("default mode must produce an empty augmentation (no-op)")
	}
}

func TestModeSystemPrompt_UnknownIsEmpty(t *testing.T) {
	// An unrecognised mode must NOT inject anything — fail safe to plain chat.
	if modeSystemPrompt("not-a-real-mode") != "" {
		t.Fatal("unknown mode must produce an empty augmentation (no-op)")
	}
}

func TestModeSystemPrompt_WhitespaceTolerant(t *testing.T) {
	if modeSystemPrompt("  perplexity  ") == "" {
		t.Fatal("mode lookup must tolerate surrounding whitespace")
	}
}
