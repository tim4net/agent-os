package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// AgyHarness drives the Antigravity CLI (`agy`) as a local shell subprocess.
//
// Unlike the HTTP/WebSocket harnesses, agy is a coding agent invoked on the
// box where Agent OS runs. Chat is a one-shot `agy --print` call: agy buffers
// its full response and prints it on completion, so this harness collects the
// output, strips any markdown code fences agy adds, and emits it as a single
// content chunk followed by Done (no synthetic token streaming).
//
// Authentication is AMBIENT. agy 1.0.5 authenticates through Antigravity's own
// Google login, resolved at runtime via the OS keyring/Secret Service (reached
// over DBUS, independent of $HOME). It does NOT read an API key from
// GEMINI_API_KEY / GOOGLE_API_KEY, does NOT consume a Vertex service account,
// and does NOT ingest ~/.gemini/oauth_creds.json when the keyring is absent —
// all four were measured against agy 1.0.5. Therefore this harness carries no
// secret: it relies on the agy login present in the server process's
// environment. When agy is not logged in, a chat returns a clear
// "not authenticated" error instead of hanging on the interactive browser flow.
//
// Configuration is read from the Init config map (keys below) and falls back to
// environment variables, then to defaults:
//   - "agy_bin"            / AGENTOS_AGY_BIN            (default: "agy")
//   - "model"              / AGENTOS_AGY_MODEL          (default: "" → agy's default)
//   - "workdir"            / AGENTOS_AGY_WORKDIR        (default: process cwd)
//   - "print_timeout_sec"  / AGENTOS_AGY_PRINT_TIMEOUT  (default: 300)
//   - "skip_permissions"   / AGENTOS_AGY_SKIP_PERMS     (default: false)
type AgyHarness struct {
	binPath         string
	model           string
	workdir         string
	skipPermissions bool
	printTimeout    time.Duration
}

// NewAgyHarness constructs an AgyHarness with defaults; Init applies config.
func NewAgyHarness() Harness {
	return &AgyHarness{
		binPath:      "agy",
		printTimeout: 5 * time.Minute,
	}
}

func (a *AgyHarness) Name() string { return "agy" }

func (a *AgyHarness) Init(config map[string]any) error {
	// agy is local; base_url is meaningless and intentionally ignored.

	a.binPath = firstNonEmpty(strConfig(config, "agy_bin"), os.Getenv("AGENTOS_AGY_BIN"), "agy")
	a.model = firstNonEmpty(strConfig(config, "model"), os.Getenv("AGENTOS_AGY_MODEL"))
	a.workdir = firstNonEmpty(strConfig(config, "workdir"), os.Getenv("AGENTOS_AGY_WORKDIR"))

	a.printTimeout = 5 * time.Minute
	if secs := firstNonEmpty(strConfig(config, "print_timeout_sec"), os.Getenv("AGENTOS_AGY_PRINT_TIMEOUT")); secs != "" {
		var n int
		if _, err := fmt.Sscanf(secs, "%d", &n); err == nil && n > 0 {
			a.printTimeout = time.Duration(n) * time.Second
		}
	}

	if skip := firstNonEmpty(strConfig(config, "skip_permissions"), os.Getenv("AGENTOS_AGY_SKIP_PERMS")); skip == "true" || skip == "1" {
		a.skipPermissions = true
	}

	return nil
}

// Health runs `agy --version`. A clean exit with a version string means the
// binary is installed and runnable. This deliberately does NOT prove auth
// (which only a real chat can), keeping the health check cheap and free of any
// risk of triggering the interactive login flow.
func (a *AgyHarness) Health(ctx context.Context) (*HealthStatus, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, a.binPath, "--version")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return &HealthStatus{Status: "offline"}, nil
	}

	version := strings.TrimSpace(string(out))
	return &HealthStatus{Status: "online", Version: version}, nil
}

func (a *AgyHarness) VersionInfo(ctx context.Context) (*VersionInfo, error) {
	checkedAt := time.Now().UTC()
	unknown := &VersionInfo{Current: "", Source: "unknown", CheckedAt: checkedAt}

	health, err := a.Health(ctx)
	if err != nil || health == nil || health.Version == "" {
		return unknown, nil
	}
	return &VersionInfo{Current: health.Version, Source: "cli", CheckedAt: checkedAt}, nil
}

// Chat sends the conversation to `agy --print` as a single one-shot prompt.
// agy buffers the entire response, so the harness emits one content chunk then
// Done. The output is stripped of any wrapping markdown code fences.
func (a *AgyHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	prompt := buildAgyPrompt(messages, opts.SystemPrompt)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("agy: no prompt to send")
	}

	model := opts.Model
	if model == "" {
		model = a.model
	}

	args := make([]string, 0, 6)
	if model != "" {
		args = append(args, "--model", model)
	}
	if a.skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if a.workdir != "" {
		args = append(args, "--add-dir", a.workdir)
	}
	// --print must carry the prompt as a single argv element (no shell), so a
	// chat message can never be interpreted as a flag injection or shell metachar.
	args = append(args, "--print", prompt)

	ch := make(chan ChatChunk, 4)

	go func() {
		defer close(ch)

		cctx, cancel := context.WithTimeout(ctx, a.printTimeout)
		defer cancel()

		cmd := exec.CommandContext(cctx, a.binPath, args...)
		cmd.Env = os.Environ()
		if a.workdir != "" {
			cmd.Dir = a.workdir
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		out := stdout.String()

		// Surface a non-authenticated environment as a clear, actionable error
		// rather than letting the URL/paste-code prompt masquerade as content.
		if isAgyAuthPrompt(out) || isAgyAuthPrompt(stderr.String()) {
			ch <- ChatChunk{Error: fmt.Errorf("agy is not authenticated in the server environment; run `agy` and complete Google login on the host where Agent OS runs")}
			return
		}

		if cctx.Err() == context.DeadlineExceeded {
			ch <- ChatChunk{Error: fmt.Errorf("agy: response timed out after %s", a.printTimeout)}
			return
		}
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = strings.TrimSpace(out)
			}
			ch <- ChatChunk{Error: fmt.Errorf("agy --print failed: %v: %s", err, detail)}
			return
		}

		content := stripCodeFences(out)
		if content != "" {
			ch <- ChatChunk{Content: content}
		}
		ch <- ChatChunk{Done: true}
	}()

	return ch, nil
}

// ListModels runs `agy models`, which prints one model name per line.
func (a *AgyHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, a.binPath, "models")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("agy models: %w", err)
	}

	var models []ModelInfo
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		models = append(models, ModelInfo{ID: name, DisplayName: name, OwnedBy: "antigravity"})
	}
	return models, nil
}

// agyCommands are the session-management commands handled by the Agent OS
// backend for an agy agent. agy's own slash commands are interactive-TUI only
// and are not reachable through the one-shot `--print` interface.
var agyCommands = []Command{
	{Command: "/new", Description: "Start a new session"},
	{Command: "/clear", Description: "Clear messages in current conversation"},
	{Command: "/compact", Description: "Summarize and compact conversation history"},
	{Command: "/retry", Description: "Retry the last message (resend to agy)"},
	{Command: "/undo", Description: "Remove the last user/assistant exchange"},
	{Command: "/history", Description: "Show conversation history"},
	{Command: "/title", Description: "Set a title for the current session"},
	{Command: "/stop", Description: "Stop current response"},
	{Command: "/save", Description: "Export conversation to Obsidian"},
	{Command: "/models", Description: "List available agy models"},
}

func (a *AgyHarness) Commands() []Command { return agyCommands }

// Close is a no-op: each chat is a self-contained subprocess.
func (a *AgyHarness) Close() error { return nil }

// --- helpers --------------------------------------------------------------

// buildAgyPrompt flattens a multi-turn conversation into a single prompt for
// agy's one-shot --print mode. The system prompt (if any) leads; prior turns
// are role-tagged for context; the final user turn is presented plainly.
func buildAgyPrompt(messages []ChatMessage, systemPrompt string) string {
	var b strings.Builder
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		b.WriteString(sp)
		b.WriteString("\n\n")
	}

	// If there's only a single user message, send it bare (most common case).
	if len(messages) == 1 && messages[0].Role == "user" {
		b.WriteString(messages[0].Content)
		return strings.TrimSpace(b.String())
	}

	for i, m := range messages {
		role := m.Role
		switch role {
		case "user":
			role = "User"
		case "assistant":
			role = "Assistant"
		case "system":
			// fold a mid-stream system message in as context
			role = "System"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		if i < len(messages)-1 {
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// stripCodeFences removes a single wrapping markdown code fence (```lang ... ```)
// that agy frequently adds around --print output, even when asked not to. Only a
// fence that brackets the WHOLE output is removed; inline/embedded fenced blocks
// are preserved.
func stripCodeFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}
	// First line is the opening fence (optionally with a language tag).
	// Require a closing fence on the last line to treat it as a wrapper.
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != "```" {
		return trimmed
	}
	inner := lines[1 : len(lines)-1]
	return strings.TrimSpace(strings.Join(inner, "\n"))
}

// isAgyAuthPrompt detects agy's interactive login output, which appears when no
// valid credential is present in the environment.
func isAgyAuthPrompt(s string) bool {
	return strings.Contains(s, "Authentication required") ||
		strings.Contains(s, "Please visit the URL to log in") ||
		strings.Contains(s, "authentication timed out")
}

func strConfig(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	if v, ok := config[key].(string); ok {
		return v
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func init() {
	Register("agy", NewAgyHarness)
}
