package harness

import (
	"context"
	"strings"
	"testing"
)

func TestAgyName(t *testing.T) {
	a := NewAgyHarness()
	if a.Name() != "agy" {
		t.Fatalf("Name() = %q, want %q", a.Name(), "agy")
	}
}

func TestAgyInitConfig(t *testing.T) {
	a := &AgyHarness{}
	err := a.Init(map[string]any{
		"agy_bin":           "/usr/local/bin/agy",
		"model":             "Gemini 3.1 Pro (High)",
		"workdir":           "/tmp/work",
		"print_timeout_sec": "120",
		"skip_permissions":  "true",
	})
	if err != nil {
		t.Fatalf("Init err: %v", err)
	}
	if a.binPath != "/usr/local/bin/agy" {
		t.Errorf("binPath = %q", a.binPath)
	}
	if a.model != "Gemini 3.1 Pro (High)" {
		t.Errorf("model = %q", a.model)
	}
	if a.workdir != "/tmp/work" {
		t.Errorf("workdir = %q", a.workdir)
	}
	if a.printTimeout.Seconds() != 120 {
		t.Errorf("printTimeout = %v, want 120s", a.printTimeout)
	}
	if !a.skipPermissions {
		t.Errorf("skipPermissions = false, want true")
	}
}

func TestAgyInitDefaults(t *testing.T) {
	a := &AgyHarness{}
	if err := a.Init(map[string]any{}); err != nil {
		t.Fatalf("Init err: %v", err)
	}
	if a.binPath != "agy" {
		t.Errorf("default binPath = %q, want agy", a.binPath)
	}
	if a.printTimeout.Minutes() != 5 {
		t.Errorf("default printTimeout = %v, want 5m", a.printTimeout)
	}
	if a.skipPermissions {
		t.Errorf("default skipPermissions = true, want false")
	}
}

func TestAgyInitNilConfig(t *testing.T) {
	a := &AgyHarness{}
	if err := a.Init(nil); err != nil {
		t.Fatalf("Init(nil) err: %v", err)
	}
	if a.binPath != "agy" {
		t.Errorf("binPath = %q after nil config", a.binPath)
	}
}

func TestStripCodeFences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no fence", "hello world", "hello world"},
		{"plain fence", "```\nhello\n```", "hello"},
		{"lang fence", "```go\nfmt.Println(1)\n```", "fmt.Println(1)"},
		{"lang fence html", "```html\n<div>x</div>\n```", "<div>x</div>"},
		{"surrounding whitespace", "  \n```\nhi\n```\n  ", "hi"},
		{"multiline body", "```python\na = 1\nb = 2\n```", "a = 1\nb = 2"},
		{"no closing fence is left intact", "```go\nno end", "```go\nno end"},
		{"inner fenced block preserved", "intro\n```go\nx\n```\noutro", "intro\n```go\nx\n```\noutro"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripCodeFences(tc.in); got != tc.want {
				t.Errorf("stripCodeFences(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildAgyPromptSingleUser(t *testing.T) {
	got := buildAgyPrompt([]ChatMessage{{Role: "user", Content: "what is 2+2?"}}, "")
	if got != "what is 2+2?" {
		t.Errorf("single user prompt = %q", got)
	}
}

func TestBuildAgyPromptWithSystem(t *testing.T) {
	got := buildAgyPrompt([]ChatMessage{{Role: "user", Content: "hi"}}, "Be terse.")
	if !strings.HasPrefix(got, "Be terse.") {
		t.Errorf("system prompt not leading: %q", got)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("user content missing: %q", got)
	}
}

func TestBuildAgyPromptMultiTurn(t *testing.T) {
	got := buildAgyPrompt([]ChatMessage{
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

func TestBuildAgyPromptEmpty(t *testing.T) {
	if got := buildAgyPrompt(nil, ""); got != "" {
		t.Errorf("empty conversation = %q, want empty", got)
	}
}

func TestIsAgyAuthPrompt(t *testing.T) {
	auth := "Authentication required. Please visit the URL to log in:\n  https://accounts.google.com/..."
	if !isAgyAuthPrompt(auth) {
		t.Errorf("auth prompt not detected")
	}
	if !isAgyAuthPrompt("Error: authentication timed out.") {
		t.Errorf("timeout not detected")
	}
	if isAgyAuthPrompt("The answer is 4.") {
		t.Errorf("normal content misclassified as auth prompt")
	}
}

func TestAgyCommandsNonEmpty(t *testing.T) {
	a := NewAgyHarness()
	cmds := a.Commands()
	if len(cmds) == 0 {
		t.Fatal("Commands() empty")
	}
	found := false
	for _, c := range cmds {
		if c.Command == "/new" {
			found = true
		}
		if c.Command == "" || c.Description == "" {
			t.Errorf("command with empty field: %+v", c)
		}
	}
	if !found {
		t.Error("expected /new command")
	}
}

// Chat with an empty conversation must return an error, not spawn agy.
func TestAgyChatEmptyPrompt(t *testing.T) {
	a := NewAgyHarness()
	_, err := a.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for empty prompt, got nil")
	}
}

func TestAgyCloseNoop(t *testing.T) {
	a := NewAgyHarness()
	if err := a.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want x", got)
	}
	if got := firstNonEmpty("", "  "); got != "" {
		t.Errorf("firstNonEmpty all-empty = %q, want empty", got)
	}
}
