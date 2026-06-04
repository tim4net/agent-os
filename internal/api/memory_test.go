package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Memory Tree handler tests — pure-filesystem, no DB required.
// ---------------------------------------------------------------------------

// newMemoryAPIWithVault builds a MemoryAPI rooted at the given path. queries is
// nil because the Tree handler never touches the database.
func newMemoryAPIWithVault(path string) *MemoryAPI {
	return NewMemoryAPI(nil, path, "", "")
}

// TestTree_MissingVault_ReturnsEmptyOK proves:
// GET /tree returns 200 with an empty JSON array (not a 500) when the configured
// vault directory does not exist — a legitimate first-run state. Regression test
// for the Knowledge > Files "API error 500" defect.
func TestTree_MissingVault_ReturnsEmptyOK(t *testing.T) {
	// A path under a temp dir that we deliberately do NOT create.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	m := newMemoryAPIWithVault(missing)

	req := httptest.NewRequest("GET", "/tree", nil)
	rec := httptest.NewRecorder()
	m.MemoryRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for missing vault, got %d; body: %s", rec.Code, rec.Body.String())
	}
	// Must be a JSON array, never null — assert the raw body is [] so a
	// regression that encodes nil (-> "null") is caught.
	if got := rec.Body.String(); got != "[]\n" && got != "[]" {
		t.Fatalf("expected empty JSON array for missing vault, got %q", got)
	}
	var nodes []TreeNode
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("response was not a JSON array: %v; body: %s", err, rec.Body.String())
	}
	if len(nodes) != 0 {
		t.Fatalf("expected empty tree, got %d nodes", len(nodes))
	}
}

// TestTree_EmptyVault_ReturnsEmptyArrayNotNull proves:
// An existing but empty vault returns [] (a non-nil array), never null — so the
// UI always receives an iterable.
func TestTree_EmptyVault_ReturnsEmptyArrayNotNull(t *testing.T) {
	vault := t.TempDir() // exists, no files
	m := newMemoryAPIWithVault(vault)

	req := httptest.NewRequest("GET", "/tree", nil)
	rec := httptest.NewRecorder()
	m.MemoryRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" && got != "[]" {
		t.Fatalf("expected empty JSON array, got %q", got)
	}
}

// TestTree_PopulatedVault_ListsNotes proves the happy path still works: notes
// written into the vault are returned by the tree endpoint.
func TestTree_PopulatedVault_ListsNotes(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "welcome.md"), []byte("# Welcome\n"), 0o644); err != nil {
		t.Fatalf("seed note: %v", err)
	}
	m := newMemoryAPIWithVault(vault)

	req := httptest.NewRequest("GET", "/tree", nil)
	rec := httptest.NewRecorder()
	m.MemoryRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var nodes []TreeNode
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "welcome.md" || nodes[0].Type != "file" {
		t.Fatalf("expected one file node 'welcome.md', got %+v", nodes)
	}
}

// TestTree_PathTraversal_Denied proves the security guard is intact: a subPath
// escaping the vault base is rejected (not silently served).
func TestTree_PathTraversal_Denied(t *testing.T) {
	vault := t.TempDir()
	m := newMemoryAPIWithVault(vault)

	req := httptest.NewRequest("GET", "/tree?path=../../etc", nil)
	rec := httptest.NewRecorder()
	m.MemoryRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for path traversal, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestTree_SiblingPrefixEscape_Denied proves the containment check is robust to
// the sibling-prefix pitfall: a vault base like ".../vault" must NOT treat a
// sibling like ".../vault-evil" as inside. A naive strings.HasPrefix would have
// allowed this; isWithinBase (filepath.Rel based) must reject it.
func TestTree_SiblingPrefixEscape_Denied(t *testing.T) {
	parent := t.TempDir()
	vault := filepath.Join(parent, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	// Create a sibling dir that shares the vault's name prefix, with a file in it.
	sibling := filepath.Join(parent, "vault-evil")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "secret.md"), []byte("# secret\n"), 0o644); err != nil {
		t.Fatalf("seed sibling secret: %v", err)
	}

	m := newMemoryAPIWithVault(vault)
	// ../vault-evil resolves to the sibling, which shares the "vault" prefix.
	req := httptest.NewRequest("GET", "/tree?path=../vault-evil", nil)
	rec := httptest.NewRecorder()
	m.MemoryRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for sibling-prefix escape, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestIsWithinBase_Boundaries unit-tests the containment helper directly.
func TestIsWithinBase_Boundaries(t *testing.T) {
	cases := []struct {
		name     string
		base     string
		path     string
		expected bool
	}{
		{"base itself", "/data/vault", "/data/vault", true},
		{"direct child", "/data/vault", "/data/vault/note.md", true},
		{"nested child", "/data/vault", "/data/vault/a/b/c.md", true},
		{"sibling prefix", "/data/vault", "/data/vault-evil", false},
		{"sibling prefix file", "/data/vault", "/data/vault-evil/secret.md", false},
		{"parent", "/data/vault", "/data", false},
		{"unrelated", "/data/vault", "/etc/passwd", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isWithinBase(c.path, c.base); got != c.expected {
				t.Fatalf("isWithinBase(%q, %q) = %v, want %v", c.path, c.base, got, c.expected)
			}
		})
	}
}
