package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClaudeName(t *testing.T) {
	c := NewClaudeHarness()
	if c.Name() != "claude" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "claude")
	}
}

func TestClaudeInitConfig(t *testing.T) {
	c := &ClaudeHarness{}
	err := c.Init(map[string]any{
		"claude_bin":        "/usr/local/bin/claude",
		"model":             "sonnet",
		"workdir":           "/tmp/work",
		"print_timeout_sec": "120",
		"skip_permissions":  "true",
		"api_key":           "sk-vault-123",
	})
	if err != nil {
		t.Fatalf("Init err: %v", err)
	}
	if c.binPath != "/usr/local/bin/claude" {
		t.Errorf("binPath = %q", c.binPath)
	}
	if c.model != "sonnet" {
		t.Errorf("model = %q", c.model)
	}
	if c.workdir != "/tmp/work" {
		t.Errorf("workdir = %q", c.workdir)
	}
	if c.printTimeout.Seconds() != 120 {
		t.Errorf("printTimeout = %v, want 120s", c.printTimeout)
	}
	if !c.skipPermissions {
		t.Errorf("skipPermissions = false, want true")
	}
	if c.apiKey != "sk-vault-123" {
		t.Errorf("apiKey = %q, want sk-vault-123 (vault credential must be captured)", c.apiKey)
	}
}

func TestClaudeInitDefaults(t *testing.T) {
	c := &ClaudeHarness{}
	if err := c.Init(map[string]any{}); err != nil {
		t.Fatalf("Init err: %v", err)
	}
	if c.binPath != "claude" {
		t.Errorf("default binPath = %q, want claude", c.binPath)
	}
	if c.printTimeout.Minutes() != 5 {
		t.Errorf("default printTimeout = %v, want 5m", c.printTimeout)
	}
	if c.skipPermissions {
		t.Errorf("default skipPermissions = true, want false")
	}
	if c.apiKey != "" {
		t.Errorf("default apiKey = %q, want empty (default-deny)", c.apiKey)
	}
}

func TestClaudeInitNilConfig(t *testing.T) {
	c := &ClaudeHarness{}
	if err := c.Init(nil); err != nil {
		t.Fatalf("Init(nil) err: %v", err)
	}
	if c.binPath != "claude" {
		t.Errorf("binPath = %q after nil config", c.binPath)
	}
}

// --- buildClaudePrompt ----------------------------------------------------

func TestBuildClaudePromptSingleUser(t *testing.T) {
	got := buildClaudePrompt([]ChatMessage{{Role: "user", Content: "what is 2+2?"}}, "")
	if got != "what is 2+2?" {
		t.Errorf("single user prompt = %q", got)
	}
}

func TestBuildClaudePromptWithSystem(t *testing.T) {
	got := buildClaudePrompt([]ChatMessage{{Role: "user", Content: "hi"}}, "Be terse.")
	if !strings.HasPrefix(got, "Be terse.") {
		t.Errorf("system prompt not leading: %q", got)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("user content missing: %q", got)
	}
}

func TestBuildClaudePromptMultiTurn(t *testing.T) {
	got := buildClaudePrompt([]ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "ack"},
		{Role: "user", Content: "second"},
	}, "")
	if !strings.Contains(got, "User: first") {
		t.Errorf("missing tagged user turn: %q", got)
	}
	if !strings.Contains(got, "Assistant: ack") {
		t.Errorf("missing tagged assistant turn: %q", got)
	}
	if !strings.Contains(got, "User: second") {
		t.Errorf("missing final user turn: %q", got)
	}
}

func TestBuildClaudePromptEmpty(t *testing.T) {
	if got := buildClaudePrompt(nil, ""); got != "" {
		t.Errorf("empty conversation = %q, want empty", got)
	}
}

// --- credential injection (vault → ANTHROPIC_API_KEY) ---------------------

func TestBuildClaudeEnvAddsKey(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/tmp"}
	env := buildClaudeEnv(base, "sk-vault-abc")
	if !envHasKey(env, "ANTHROPIC_API_KEY", "sk-vault-abc") {
		t.Errorf("env missing injected ANTHROPIC_API_KEY: %v", env)
	}
	// base entries preserved
	if !envHasKey(env, "PATH", "/usr/bin") {
		t.Errorf("base PATH lost: %v", env)
	}
}

func TestBuildClaudeEnvOverridesExistingKey(t *testing.T) {
	base := []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=ambient-old"}
	env := buildClaudeEnv(base, "sk-vault-new")
	if envHasKey(env, "ANTHROPIC_API_KEY", "ambient-old") {
		t.Errorf("ambient (pre-existing) key must be overridden by vault value: %v", env)
	}
	if !envHasKey(env, "ANTHROPIC_API_KEY", "sk-vault-new") {
		t.Errorf("vault key not applied: %v", env)
	}
	// exactly one ANTHROPIC_API_KEY entry
	count := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 ANTHROPIC_API_KEY entry, got %d: %v", count, env)
	}
}

func TestBuildClaudeEnvNoKeyIsPassthrough(t *testing.T) {
	base := []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=ambient"}
	env := buildClaudeEnv(base, "")
	// empty apiKey → ambient env untouched (no override, no injection)
	if !envHasKey(env, "ANTHROPIC_API_KEY", "ambient") {
		t.Errorf("ambient key must be preserved when no vault key provided: %v", env)
	}
}

func TestBuildClaudeEnvWhitespaceOnlyKeyPassthrough(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	env := buildClaudeEnv(base, "   ")
	// whitespace-only must be treated as no key (no injection)
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Errorf("whitespace-only key must not inject ANTHROPIC_API_KEY: %v", env)
		}
	}
}

func envHasKey(env []string, key, val string) bool {
	prefix := key + "="
	for _, kv := range env {
		if kv == prefix+val {
			return true
		}
	}
	return false
}

// --- auth-error detection -------------------------------------------------

func TestIsClaudeAuthError(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Error: Invalid API key provided.", true},
		{"ANTHROPIC_API_KEY is not set", true},
		{"Authentication error: token expired", true},
		{"You are not logged in. Please run `claude login`.", true},
		{"401 Unauthorized", true},
		{"The answer is 4.", false},
		{"syntax error in file foo.go", false},
	}
	for _, tc := range cases {
		if got := isClaudeAuthError(tc.in); got != tc.want {
			t.Errorf("isClaudeAuthError(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// --- commands / close / models --------------------------------------------

func TestClaudeCommandsNonEmpty(t *testing.T) {
	c := NewClaudeHarness()
	cmds := c.Commands()
	if len(cmds) == 0 {
		t.Fatal("Commands() empty")
	}
	found := false
	for _, cmd := range cmds {
		if cmd.Command == "/new" {
			found = true
		}
		if cmd.Command == "" || cmd.Description == "" {
			t.Errorf("command with empty field: %+v", cmd)
		}
	}
	if !found {
		t.Error("expected /new command")
	}
}

func TestClaudeCloseNoop(t *testing.T) {
	c := NewClaudeHarness()
	if err := c.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestClaudeListModels(t *testing.T) {
	c := NewClaudeHarness()
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels err: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("ListModels returned no models")
	}
	seen := map[string]bool{}
	for _, m := range models {
		if m.ID == "" {
			t.Errorf("model with empty ID: %+v", m)
		}
		seen[m.ID] = true
	}
	for _, want := range []string{"sonnet", "opus"} {
		if !seen[want] {
			t.Errorf("expected alias %q in models: %+v", want, models)
		}
	}
}

// Chat with an empty conversation must return an error, not spawn claude.
func TestClaudeChatEmptyPrompt(t *testing.T) {
	c := NewClaudeHarness()
	_, err := c.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for empty prompt, got nil")
	}
}

// --- hermetic subprocess tests --------------------------------------------
//
// These point claude_bin at a stub script so the full Chat path (argv
// construction, env injection, output collection, completion) is exercised
// WITHOUT a live Anthropic API key. They prove "tasks sent to it return
// results".

// writeStubBin writes an executable that asserts its argv/env and prints a
// canned response. Returns its path.
func writeStubBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub-binary tests are POSIX-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

// TestClaudeChatReturnsResult proves a dispatched task returns content + Done.
func TestClaudeChatReturnsResult(t *testing.T) {
	bin := writeStubBin(t, `printf '42'`)
	c := &ClaudeHarness{binPath: bin, printTimeout: 10 * time.Second}
	if err := c.Init(map[string]any{"claude_bin": bin}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ch, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "what is 6*7?"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}

	var content string
	gotDone := false
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("chunk error: %v", chunk.Error)
		}
		if chunk.Done {
			gotDone = true
		}
		if chunk.Content != "" {
			content = chunk.Content
		}
	}
	if !gotDone {
		t.Error("expected Done chunk")
	}
	if content != "42" {
		t.Errorf("content = %q, want 42", content)
	}
}

// TestClaudeChatInjectsVaultKey proves the vault-sourced credential reaches the
// subprocess as ANTHROPIC_API_KEY (the "credentials sourced from the vault"
// acceptance criterion).
func TestClaudeChatInjectsVaultKey(t *testing.T) {
	// Stub writes the key it receives to a file we then read back.
	keyFile := filepath.Join(t.TempDir(), "key.txt")
	bin := writeStubBin(t, `printf '%s' "$ANTHROPIC_API_KEY" > "`+keyFile+`"`+"\n"+`printf 'ok'`)

	c := &ClaudeHarness{printTimeout: 10 * time.Second}
	if err := c.Init(map[string]any{
		"claude_bin": bin,
		"api_key":    "sk-from-vault-XYZ",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ch, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("chunk error: %v", chunk.Error)
		}
	}

	got, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if string(got) != "sk-from-vault-XYZ" {
		t.Errorf("ANTHROPIC_API_KEY in subprocess = %q, want sk-from-vault-XYZ", string(got))
	}
}

// TestClaudeChatNoKeyUsesAmbient proves that without a vault key the subprocess
// inherits the ambient ANTHROPIC_API_KEY (env passthrough, no clobber).
func TestClaudeChatNoKeyUsesAmbient(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key.txt")
	bin := writeStubBin(t, `printf '%s' "$ANTHROPIC_API_KEY" > "`+keyFile+`"`+"\n"+`printf 'ok'`)

	t.Setenv("ANTHROPIC_API_KEY", "ambient-ABC")

	c := &ClaudeHarness{printTimeout: 10 * time.Second}
	if err := c.Init(map[string]any{"claude_bin": bin}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ch, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("chunk error: %v", chunk.Error)
		}
	}

	got, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if string(got) != "ambient-ABC" {
		t.Errorf("ambient ANTHROPIC_API_KEY = %q, want ambient-ABC (passthrough)", string(got))
	}
}

// TestClaudeChatSurfacesAuthError proves an authentication failure is surfaced
// as a clear error chunk rather than masquerading as content.
func TestClaudeChatSurfacesAuthError(t *testing.T) {
	bin := writeStubBin(t, `echo 'Invalid API key provided.' >&2`+"\n"+`exit 1`)
	c := &ClaudeHarness{printTimeout: 10 * time.Second}
	if err := c.Init(map[string]any{"claude_bin": bin}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ch, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}
	var hitAuthErr bool
	for chunk := range ch {
		if chunk.Error != nil {
			if strings.Contains(chunk.Error.Error(), "not authenticated") {
				hitAuthErr = true
			}
		}
		if chunk.Done {
			t.Error("must not emit Done when auth fails")
		}
	}
	if !hitAuthErr {
		t.Fatal("expected a 'not authenticated' error chunk")
	}
}

// TestClaudeChatBuildsFlags proves model/skip-perms/add-dir flags reach argv.
func TestClaudeChatBuildsFlags(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	// Echo all args to a file so we can inspect what claude was invoked with.
	bin := writeStubBin(t, `printf '%s\n' "$@" > "`+argsFile+`"`+"\n"+`printf 'ok'`)

	workdir := t.TempDir() // must exist: the harness sets cmd.Dir = workdir
	c := &ClaudeHarness{printTimeout: 10 * time.Second}
	if err := c.Init(map[string]any{
		"claude_bin":       bin,
		"model":            "opus",
		"workdir":          workdir,
		"skip_permissions": "true",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ch, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hello"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("chunk error: %v", chunk.Error)
		}
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	wantTokens := []string{"--model", "opus", "--dangerously-skip-permissions", "--add-dir", workdir, "--print", "hello"}
	argStr := string(args)
	for _, tok := range wantTokens {
		if !strings.Contains(argStr, tok+"\n") {
			t.Errorf("argv missing token %q\n--- argv ---\n%s", tok, argStr)
		}
	}
}

// TestClaudeChatTimeout proves a hung subprocess is cancelled and surfaced.
// `exec sleep` replaces the stub shell with a single sleep process so the
// context kill closes the stdout pipe promptly (no orphaned grandchild holds
// it open), mirroring how a real `claude --print` behaves.
func TestClaudeChatTimeout(t *testing.T) {
	bin := writeStubBin(t, `exec sleep 3`)
	c := &ClaudeHarness{printTimeout: 300 * time.Millisecond}
	if err := c.Init(map[string]any{"claude_bin": bin}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	c.printTimeout = 300 * time.Millisecond

	ch, err := c.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat err: %v", err)
	}
	var hitTimeout bool
	for chunk := range ch {
		if chunk.Error != nil && strings.Contains(chunk.Error.Error(), "timed out") {
			hitTimeout = true
		}
	}
	if !hitTimeout {
		t.Fatal("expected a timeout error")
	}
}

// --- registration ---------------------------------------------------------

// TestRegistry_ClaudeRegistered verifies the claude harness self-registers via
// init() like the other built-in kinds. Without this, an agent row with
// harness='claude' fails at registry.Get() in the health loop and chat path.
func TestRegistry_ClaudeRegistered(t *testing.T) {
	names := DefaultRegistry.Names()
	found := false
	for _, n := range names {
		if n == "claude" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("claude not in DefaultRegistry names: %v", names)
	}
	h, err := DefaultRegistry.Get("claude")
	if err != nil {
		t.Fatalf("Get(\"claude\") failed: %v", err)
	}
	if h == nil {
		t.Fatal("Get(\"claude\") returned nil harness")
	}
	if h.Name() != "claude" {
		t.Errorf("claude harness Name() = %q, want \"claude\"", h.Name())
	}
}

// smoke: if the real claude binary is present, Health must report online with a
// version. Skipped when claude is absent so CI without the CLI stays green.
func TestClaudeHealthRealBinary(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not installed; skipping real health check")
	}
	c := NewClaudeHarness()
	st, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health err: %v", err)
	}
	if st.Status != "online" {
		t.Fatalf("Health status = %q, want online", st.Status)
	}
	if st.Version == "" {
		t.Error("expected non-empty version from claude --version")
	}
	t.Logf("claude health: %s", st.Version)
}
