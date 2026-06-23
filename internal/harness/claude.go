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

// ClaudeHarness drives the Claude Code CLI (`claude`) as a local shell
// subprocess, symmetric with the agy harness.
//
// Claude Code is a coding agent invoked on the box where Agent OS runs. Chat is
// a one-shot `claude --print` call: the CLI buffers its full response and prints
// it on completion, so this harness collects the output and emits it as a single
// content chunk followed by Done (no synthetic token streaming).
//
// Authentication is sourced from the vault (default-deny), with an ambient
// fallback. buildHarnessConfig injects a granted "credential" resource as
// config["api_key"]; this harness promotes it to the ANTHROPIC_API_KEY
// environment variable on the subprocess, which is how Claude Code authenticates
// in non-interactive --print mode. When no credential is granted the subprocess
// inherits the ambient environment (an existing ANTHROPIC_API_KEY or a prior
// `claude login`). An unauthenticated run surfaces a clear "not authenticated"
// error rather than hanging or emitting a partial response.
//
// Configuration is read from the Init config map (keys below) and falls back to
// environment variables, then to defaults:
//   - "claude_bin"           / AGENTOS_CLAUDE_BIN           (default: "claude")
//   - "model"                / AGENTOS_CLAUDE_MODEL         (default: "" → claude's default)
//   - "workdir"              / AGENTOS_CLAUDE_WORKDIR       (default: process cwd)
//   - "print_timeout_sec"    / AGENTOS_CLAUDE_PRINT_TIMEOUT (default: 300)
//   - "skip_permissions"     / AGENTOS_CLAUDE_SKIP_PERMS    (default: false)
//   - "api_key"              — vault-sourced; never an env var on the server process
type ClaudeHarness struct {
	binPath         string
	model           string
	workdir         string
	skipPermissions bool
	printTimeout    time.Duration
	apiKey          string // vault-sourced ANTHROPIC_API_KEY ("" → ambient)
}

// NewClaudeHarness constructs a ClaudeHarness with defaults; Init applies config.
func NewClaudeHarness() Harness {
	return &ClaudeHarness{
		binPath:      "claude",
		printTimeout: 5 * time.Minute,
	}
}

func (c *ClaudeHarness) Name() string { return "claude" }

func (c *ClaudeHarness) Init(config map[string]any) error {
	// claude is local; base_url is meaningless and intentionally ignored.

	c.binPath = firstNonEmpty(strConfig(config, "claude_bin"), os.Getenv("AGENTOS_CLAUDE_BIN"), "claude")
	c.model = firstNonEmpty(strConfig(config, "model"), os.Getenv("AGENTOS_CLAUDE_MODEL"))
	c.workdir = firstNonEmpty(strConfig(config, "workdir"), os.Getenv("AGENTOS_CLAUDE_WORKDIR"))
	// Vault-sourced credential. buildHarnessConfig injects a granted "credential"
	// resource here (default-deny). It is intentionally NOT read from a server env
	// var so a credential never silently leaks from the host environment.
	c.apiKey = strConfig(config, "api_key")

	c.printTimeout = 5 * time.Minute
	if secs := firstNonEmpty(strConfig(config, "print_timeout_sec"), os.Getenv("AGENTOS_CLAUDE_PRINT_TIMEOUT")); secs != "" {
		var n int
		if _, err := fmt.Sscanf(secs, "%d", &n); err == nil && n > 0 {
			c.printTimeout = time.Duration(n) * time.Second
		}
	}

	if skip := firstNonEmpty(strConfig(config, "skip_permissions"), os.Getenv("AGENTOS_CLAUDE_SKIP_PERMS")); skip == "true" || skip == "1" {
		c.skipPermissions = true
	}

	return nil
}

// Health runs `claude --version`. A clean exit with a version string means the
// binary is installed and runnable. This deliberately does NOT prove auth (which
// only a real chat can), keeping the health check cheap and free of any risk of
// triggering an interactive login flow.
func (c *ClaudeHarness) Health(ctx context.Context) (*HealthStatus, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, c.binPath, "--version")
	cmd.Env = buildClaudeEnv(os.Environ(), c.apiKey)
	out, err := cmd.Output()
	if err != nil {
		return &HealthStatus{Status: "offline"}, nil
	}

	version := strings.TrimSpace(string(out))
	return &HealthStatus{Status: "online", Version: version}, nil
}

// VersionInfo reports the installed Claude Code version (wraps Health). This is
// the optional VersionProber capability; callers type-assert to use it.
func (c *ClaudeHarness) VersionInfo(ctx context.Context) (*VersionInfo, error) {
	checkedAt := time.Now().UTC()
	unknown := &VersionInfo{Current: "", Source: "unknown", CheckedAt: checkedAt}

	health, err := c.Health(ctx)
	if err != nil || health == nil || health.Version == "" {
		return unknown, nil
	}
	return &VersionInfo{Current: health.Version, Source: "cli", CheckedAt: checkedAt}, nil
}

// Chat sends the conversation to `claude --print` as a single one-shot prompt.
// Claude Code buffers the entire response, so the harness emits one content
// chunk then Done. The vault-sourced API key (if any) is injected as
// ANTHROPIC_API_KEY on the subprocess environment.
func (c *ClaudeHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	prompt := buildClaudePrompt(messages, opts.SystemPrompt)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("claude: no prompt to send")
	}

	model := opts.Model
	if model == "" {
		model = c.model
	}

	args := make([]string, 0, 6)
	if model != "" {
		args = append(args, "--model", model)
	}
	if c.skipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if c.workdir != "" {
		args = append(args, "--add-dir", c.workdir)
	}
	// --print carries the prompt as a single argv element (no shell), so a chat
	// message can never be interpreted as a flag injection or shell metachar.
	args = append(args, "--print", prompt)

	ch := make(chan ChatChunk, 4)

	go func() {
		defer close(ch)

		cctx, cancel := context.WithTimeout(ctx, c.printTimeout)
		defer cancel()

		cmd := exec.CommandContext(cctx, c.binPath, args...)
		cmd.Env = buildClaudeEnv(os.Environ(), c.apiKey)
		if c.workdir != "" {
			cmd.Dir = c.workdir
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		out := stdout.String()

		// Surface an unauthenticated environment as a clear, actionable error
		// rather than letting an auth failure masquerade as content.
		if isClaudeAuthError(out) || isClaudeAuthError(stderr.String()) {
			ch <- ChatChunk{Error: fmt.Errorf("claude is not authenticated; grant an Anthropic credential to the agent (vault) or set ANTHROPIC_API_KEY / run `claude login` on the host where Agent OS runs")}
			return
		}

		if cctx.Err() == context.DeadlineExceeded {
			ch <- ChatChunk{Error: fmt.Errorf("claude: response timed out after %s", c.printTimeout)}
			return
		}
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = strings.TrimSpace(out)
			}
			ch <- ChatChunk{Error: fmt.Errorf("claude --print failed: %v: %s", err, detail)}
			return
		}

		content := strings.TrimSpace(out)
		if content != "" {
			ch <- ChatChunk{Content: content}
		}
		ch <- ChatChunk{Done: true}
	}()

	return ch, nil
}

// ListModels returns the model aliases Claude Code accepts for --model. There is
// no stable `claude models` discovery subcommand, so this is a curated static
// list of the aliases documented by `claude --help` ("an alias for the latest
// model (e.g. 'sonnet' or 'opus')"). Each alias resolves to the current latest
// release at the CLI's discretion.
func (c *ClaudeHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	aliases := []string{"sonnet", "opus", "haiku", "default"}
	models := make([]ModelInfo, 0, len(aliases))
	for _, a := range aliases {
		models = append(models, ModelInfo{ID: a, DisplayName: "Claude " + a, OwnedBy: "anthropic"})
	}
	return models, nil
}

// claudeCommands are the session-management commands handled by the Agent OS
// backend for a claude agent. Claude Code's own slash commands are
// interactive-TUI only and are not reachable through the one-shot --print
// interface.
var claudeCommands = []Command{
	{Command: "/new", Description: "Start a new session"},
	{Command: "/clear", Description: "Clear messages in current conversation"},
	{Command: "/compact", Description: "Summarize and compact conversation history"},
	{Command: "/retry", Description: "Retry the last message (resend to claude)"},
	{Command: "/undo", Description: "Remove the last user/assistant exchange"},
	{Command: "/history", Description: "Show conversation history"},
	{Command: "/title", Description: "Set a title for the current session"},
	{Command: "/stop", Description: "Stop current response"},
	{Command: "/save", Description: "Export conversation to Obsidian"},
	{Command: "/models", Description: "List available Claude models"},
}

func (c *ClaudeHarness) Commands() []Command { return claudeCommands }

// Close is a no-op: each chat is a self-contained subprocess.
func (c *ClaudeHarness) Close() error { return nil }

// --- helpers --------------------------------------------------------------

// buildClaudeEnv returns the environment for the claude subprocess: the base
// environment with the vault-sourced API key (if any) overriding
// ANTHROPIC_API_KEY. Factored out so it is unit-testable without spawning a
// subprocess. A key of "" leaves the base environment untouched (ambient auth).
func buildClaudeEnv(base []string, apiKey string) []string {
	if strings.TrimSpace(apiKey) == "" {
		return base
	}
	env := make([]string, 0, len(base)+1)
	written := false
	for _, kv := range base {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			env = append(env, "ANTHROPIC_API_KEY="+apiKey)
			written = true
			continue
		}
		env = append(env, kv)
	}
	if !written {
		env = append(env, "ANTHROPIC_API_KEY="+apiKey)
	}
	return env
}

// buildClaudePrompt flattens a multi-turn conversation into a single prompt for
// Claude Code's one-shot --print mode. The system prompt (if any) leads; prior
// turns are role-tagged for context; the final user turn is presented plainly.
func buildClaudePrompt(messages []ChatMessage, systemPrompt string) string {
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

// isClaudeAuthError detects Claude Code authentication failures, which appear on
// stderr when no valid credential is available in --print mode (the CLI never
// opens an interactive browser flow under --print). Matches are deliberately
// conservative to avoid misclassifying normal output.
func isClaudeAuthError(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "invalid api key") ||
		strings.Contains(low, "anthropic_api_key") ||
		strings.Contains(low, "authentication error") ||
		strings.Contains(low, "not logged in") ||
		strings.Contains(low, "please run `claude login`") ||
		strings.Contains(low, "unauthorized")
}

func init() {
	Register("claude", NewClaudeHarness)
}
