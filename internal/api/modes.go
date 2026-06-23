package api

import "strings"

// Chat modes. A mode shapes how an agent responds by injecting a tailored
// system prompt. Modes are opt-in and additive — they never replace an agent's
// own system prompt, they augment it.
const (
	// ModeDefault is the standard, unmodified chat experience.
	ModeDefault = ""
	// ModePerplexity is a Perplexity-style "search computer" mode: every
	// answer must be grounded in a live web search, synthesized with inline
	// numbered citations, and followed by a sources list. It reuses the
	// agent's existing web-search tooling — no new search infra is added.
	ModePerplexity = "perplexity"
)

// modeSystemPrompt returns the system-prompt augmentation text for the given
// chat mode. It returns an empty string for the default/unknown mode so that
// normal chat is completely unaffected.
//
// The text instructs the agent to ground its answer in web search results and
// format citations inline (Perplexity-style). The agent already has web search
// available as a tool, so this is a presentation/synthesis layer, not new
// infrastructure — matching the acceptance criteria of #137.
func modeSystemPrompt(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModePerplexity, "search":
		return strings.TrimSpace(`[PERPLEXITY MODE] You are a search-grounded answer engine.
Follow these rules strictly for this turn:
1. ALWAYS use your web-search tool first to gather fresh, relevant sources for the user's question. Do not answer from memory alone.
2. Synthesize a direct, well-structured answer from the search results.
3. Cite every factual claim inline using bracketed numbers that map to the sources list, e.g. "Paris is the capital of France [1]."
4. End your reply with a numbered "Sources" list, one per line, in the form:  [n] Title — URL
5. If the search returns nothing relevant, say so explicitly rather than inventing facts or citations.
Prefer recency and authoritative sources. Be concise and factual.`)
	default:
		return ""
	}
}
