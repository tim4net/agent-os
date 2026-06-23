package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// makeFakeAgy writes a small shell script that mimics the agy CLI subcommands
// used by the harness (--version, models, --print). Returns the path.
func makeFakeAgy(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake agy binary requires sh; skipping on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "agy")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}
	return path
}

const defaultFakeAgyScript = `#!/bin/sh
case "$1" in
    --version)
        echo "fake-agy 1.0.0-test"
        ;;
    models)
        echo "gemini-2.0-flash"
        echo "gemini-2.5-pro"
        ;;
    *)
        echo "Dispatched task result: OK"
        ;;
esac
`

// ---------------------------------------------------------------------------
// Acceptance criterion 1: "agy harness dispatches a task and returns a real result"
// ---------------------------------------------------------------------------

// TestAgyRegisteredInDefaultRegistry proves the agy factory is wired into the
// global registry so that the API and watcher can resolve it by name.
func TestAgyRegisteredInDefaultRegistry(t *testing.T) {
	h, err := DefaultRegistry.Get("agy")
	if err != nil {
		t.Fatalf("agy not registered in DefaultRegistry: %v", err)
	}
	if h.Name() != "agy" {
		t.Errorf("registered harness Name() = %q, want agy", h.Name())
	}
}

// TestAgyImplementsVersionProber confirms the harness exposes upstream version
// info (used by the fleet version-visibility feature).
func TestAgyImplementsVersionProber(t *testing.T) {
	var _ VersionProber = (*AgyHarness)(nil)
}

// TestAgyChatDispatchesAndReturnsContent proves that Chat() dispatches a prompt
// to the agy binary and returns its stdout as content with a Done chunk.
func TestAgyChatDispatchesAndReturnsContent(t *testing.T) {
	fakeAgy := makeFakeAgy(t, defaultFakeAgyScript)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 10 * time.Second}

	ch, err := a.Chat(context.Background(), []ChatMessage{
		{Role: "user", Content: "do something useful"},
	}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	var content string
	var done bool
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("Chat stream error: %v", chunk.Error)
		}
		content += chunk.Content
		if chunk.Done {
			done = true
		}
	}

	if !done {
		t.Error("Chat stream ended without Done chunk")
	}
	want := "Dispatched task result: OK"
	if content != want {
		t.Errorf("Chat content = %q, want %q", content, want)
	}
}

// TestAgyChatPassesModelArg proves the model option is forwarded to the binary.
func TestAgyChatPassesModelArg(t *testing.T) {
	// The fake script echoes all its args so we can inspect what the harness sent.
	script := `#!/bin/sh
case "$1" in
    --version) echo "v" ;;
    models) echo "m" ;;
    *) for a in "$@"; do echo "$a"; done ;;
esac
`
	fakeAgy := makeFakeAgy(t, script)
	a := &AgyHarness{binPath: fakeAgy, model: "default-model", printTimeout: 10 * time.Second}

	ch, err := a.Chat(context.Background(), []ChatMessage{
		{Role: "user", Content: "hi"},
	}, ChatOptions{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	var content string
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("Chat stream error: %v", chunk.Error)
		}
		content += chunk.Content
	}

	if !strings.Contains(content, "--model") {
		t.Errorf("expected --model in dispatched args, got: %s", content)
	}
	if !strings.Contains(content, "gemini-2.5-pro") {
		t.Errorf("expected model name in dispatched args, got: %s", content)
	}
	if !strings.Contains(content, "--print") {
		t.Errorf("expected --print flag in dispatched args, got: %s", content)
	}
}

// TestAgyChatStripsCodeFences proves that markdown fences wrapping the entire
// output are stripped (the stripCodeFences path exercised through Chat).
func TestAgyChatStripsCodeFences(t *testing.T) {
	// Build the script with double-quoted Go string so backticks are literal.
	// Inside the shell script we use single-quoted echo args so the backticks
	// are NOT interpreted as command substitution by sh.
	fenceScript := "#!/bin/sh\ncase \"$1\" in\n" +
		"    --version) echo 'v' ;;\n" +
		"    *) echo '```'; echo 'hello world'; echo '```' ;;\n" +
		"esac\n"
	fakeAgy := makeFakeAgy(t, fenceScript)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 10 * time.Second}

	ch, err := a.Chat(context.Background(), []ChatMessage{
		{Role: "user", Content: "show me code"},
	}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}

	var content string
	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("Chat stream error: %v", chunk.Error)
		}
		content += chunk.Content
	}

	want := "hello world"
	if content != want {
		t.Errorf("Chat content after fence strip = %q, want %q", content, want)
	}
}

// TestAgyChatAuthPromptReturnsError proves that when the binary emits agy's
// interactive login prompt, Chat surfaces a clear error instead of passing the
// prompt as content.
func TestAgyChatAuthPromptReturnsError(t *testing.T) {
	script := `#!/bin/sh
case "$1" in
    --version) echo "v" ;;
    *) echo "Authentication required. Please visit the URL to log in:"; echo "https://accounts.google.com/..." ;;
esac
`
	fakeAgy := makeFakeAgy(t, script)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 10 * time.Second}

	ch, err := a.Chat(context.Background(), []ChatMessage{
		{Role: "user", Content: "hello"},
	}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat returned pre-stream error: %v", err)
	}

	var chatErr error
	for chunk := range ch {
		if chunk.Error != nil {
			chatErr = chunk.Error
			break
		}
	}

	if chatErr == nil {
		t.Fatal("expected auth error from Chat, got nil")
	}
	if !strings.Contains(chatErr.Error(), "not authenticated") {
		t.Errorf("expected auth-related error message, got: %v", chatErr)
	}
}

// TestAgyChatTimeout proves that a long-running binary is killed by the
// printTimeout and surfaces a timeout error.
func TestAgyChatTimeout(t *testing.T) {
	// exec sleep replaces the shell process so that SIGKILL on the PID kills
	// the sleep directly (avoids orphaned grandchild holding the stdout pipe).
	script := `#!/bin/sh
case "$1" in
    --version) echo "v" ;;
    *) exec sleep 10 ;;
esac
`
	fakeAgy := makeFakeAgy(t, script)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 200 * time.Millisecond}

	ch, err := a.Chat(context.Background(), []ChatMessage{
		{Role: "user", Content: "slow"},
	}, ChatOptions{})
	if err != nil {
		t.Fatalf("Chat returned pre-stream error: %v", err)
	}

	var chatErr error
	for chunk := range ch {
		if chunk.Error != nil {
			chatErr = chunk.Error
			break
		}
	}

	if chatErr == nil {
		t.Fatal("expected timeout error from Chat, got nil")
	}
	if !strings.Contains(chatErr.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", chatErr)
	}
}

// ---------------------------------------------------------------------------
// Acceptance criterion 2: "Fleet view shows it as online/healthy"
// ---------------------------------------------------------------------------

// TestAgyHealthOnline proves Health returns "online" with version when the
// binary is present and runs.
func TestAgyHealthOnline(t *testing.T) {
	fakeAgy := makeFakeAgy(t, defaultFakeAgyScript)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 10 * time.Second}

	health, err := a.Health(context.Background())
	if err != nil {
		t.Fatalf("Health error: %v", err)
	}
	if health.Status != "online" {
		t.Errorf("Health status = %q, want online", health.Status)
	}
	if health.Version == "" {
		t.Error("Health version empty, want non-empty")
	}
}

// TestAgyHealthOffline proves Health returns "offline" when the binary is
// missing — the agent watcher then records the agent as offline in the fleet.
func TestAgyHealthOffline(t *testing.T) {
	a := &AgyHarness{binPath: "/nonexistent/path/to/agy", printTimeout: 10 * time.Second}

	health, err := a.Health(context.Background())
	if err != nil {
		t.Fatalf("Health error: %v", err)
	}
	if health.Status != "offline" {
		t.Errorf("Health status = %q, want offline", health.Status)
	}
}

// TestAgyVersionInfoWithBinary proves the VersionProber implementation returns
// a non-empty version from the CLI.
func TestAgyVersionInfoWithBinary(t *testing.T) {
	fakeAgy := makeFakeAgy(t, defaultFakeAgyScript)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 10 * time.Second}

	vi, err := a.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error: %v", err)
	}
	if vi.Current == "" {
		t.Error("VersionInfo.Current empty, want non-empty")
	}
	if vi.Source != "cli" {
		t.Errorf("VersionInfo.Source = %q, want cli", vi.Source)
	}
}

// TestAgyVersionInfoMissingBinary proves the VersionProber returns "unknown"
// when the binary is absent (graceful degradation for the fleet view).
func TestAgyVersionInfoMissingBinary(t *testing.T) {
	a := &AgyHarness{binPath: "/nonexistent/agy", printTimeout: 10 * time.Second}
	vi, err := a.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error: %v", err)
	}
	if vi.Source != "unknown" {
		t.Errorf("VersionInfo.Source = %q, want unknown", vi.Source)
	}
}

// ---------------------------------------------------------------------------
// Acceptance criterion 3: "Credentials sourced from the vault (not hardcoded)"
// ---------------------------------------------------------------------------

// TestAgyInitReadsConfig proves the harness reads its runtime config from the
// Init config map (which buildHarnessConfig populates from the vault/resource
// store), not from hardcoded constants.
func TestAgyInitReadsConfig(t *testing.T) {
	a := &AgyHarness{}
	if err := a.Init(map[string]any{
		"agy_bin":           "/custom/agy",
		"model":             "gemini-2.5-pro",
		"workdir":           "/srv/work",
		"print_timeout_sec": "60",
		"skip_permissions":  "true",
	}); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if a.binPath != "/custom/agy" {
		t.Errorf("binPath = %q, want /custom/agy", a.binPath)
	}
	if a.model != "gemini-2.5-pro" {
		t.Errorf("model = %q, want gemini-2.5-pro", a.model)
	}
	if a.workdir != "/srv/work" {
		t.Errorf("workdir = %q, want /srv/work", a.workdir)
	}
	if a.printTimeout != 60*time.Second {
		t.Errorf("printTimeout = %v, want 60s", a.printTimeout)
	}
	if !a.skipPermissions {
		t.Error("skipPermissions = false, want true")
	}
}

// ---------------------------------------------------------------------------
// ListModels
// ---------------------------------------------------------------------------

// TestAgyListModelsWithBinary proves ListModels parses the binary's model list.
func TestAgyListModelsWithBinary(t *testing.T) {
	fakeAgy := makeFakeAgy(t, defaultFakeAgyScript)
	a := &AgyHarness{binPath: fakeAgy, printTimeout: 10 * time.Second}

	models, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("ListModels returned %d models, want 2", len(models))
	}
	if models[0].ID != "gemini-2.0-flash" {
		t.Errorf("models[0].ID = %q, want gemini-2.0-flash", models[0].ID)
	}
	if models[1].ID != "gemini-2.5-pro" {
		t.Errorf("models[1].ID = %q, want gemini-2.5-pro", models[1].ID)
	}
	if models[0].OwnedBy != "antigravity" {
		t.Errorf("models[0].OwnedBy = %q, want antigravity", models[0].OwnedBy)
	}
}
