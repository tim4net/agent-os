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
	// ModeJarvis is a voice-activated computer-control mode: the user speaks a
	// command and the agent interprets it as an actionable instruction, then
	// executes it using its existing browser/host tools (launch apps, click,
	// navigate, run commands). Destructive actions must be described and held
	// for explicit confirmation before execution. No new automation primitives
	// are introduced — the agent simply reuses the browser/terminal tooling it
	// already has (#125).
	ModeJarvis = "jarvis"
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
	case ModeJarvis, "assistant":
		return strings.TrimSpace(`[JARVIS MODE] You are a voice-activated computer-control assistant. The user is speaking commands to you through a microphone and expecting you to act on the host machine and browser.
Follow these rules strictly for this turn:
1. Interpret the user's message as an actionable command to perform on the computer (e.g. open a website, launch an application, click a button, navigate, run a shell command, search the web).
2. EXECUTE the command immediately using your available tools — browser, terminal/shell, web search, file access. Do not just describe what you would do; do it.
3. Report the outcome concisely in one or two sentences (the user is listening, not reading). State what you did and the result.
4. DESTRUCTIVE ACTIONS (deleting files, closing applications, overwriting data, running commands that modify state, formatting, etc.): Before executing, clearly state what the action will do and ask the user to confirm. Do not proceed with destructive actions until the user explicitly approves.
5. If the command is ambiguous, ask a brief clarifying question instead of guessing.
6. If a command cannot be executed with available tools, say so plainly and suggest the closest alternative.
Prefer browser/host tools you already have. Never invent results — only report what actually happened.`)
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
