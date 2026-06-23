package workflowtemplates

import (
	"strings"
	"testing"
)

// TestSEOProductionTemplateWellFormed is the headline acceptance check: the
// SEO production template (the concrete surface required by issue #133) must
// exist, validate, and carry a meaningful multi-step pipeline.
func TestSEOProductionTemplateWellFormed(t *testing.T) {
	tpl, ok := Get("seo-production")
	if !ok {
		t.Fatal("expected seo-production template to be registered")
	}

	if err := tpl.Validate(); err != nil {
		t.Fatalf("seo-production template failed validation: %v", err)
	}

	if tpl.Category != "content" {
		t.Fatalf("expected category content, got %q", tpl.Category)
	}

	// A real SEO pipeline must span research → draft → optimize → metadata.
	if len(tpl.Steps) < 4 {
		t.Fatalf("expected at least 4 steps, got %d", len(tpl.Steps))
	}

	for i, s := range tpl.Steps {
		if s.Name == "" {
			t.Fatalf("step %d has empty name", i)
		}
		if len(s.Prompt) < 40 {
			t.Fatalf("step %d (%s) prompt is too short to be actionable: %q", i, s.Name, s.Prompt)
		}
	}
}

// TestAllReturnsCopy verifies All() returns a defensive copy so the package
// registry cannot be mutated by callers.
func TestAllReturnsCopy(t *testing.T) {
	first := All()
	if len(first) == 0 {
		t.Fatal("expected at least one template")
	}
	// Mutate the returned copy.
	first[0].Name = "MUTATED"

	second := All()
	if second[0].Name == "MUTATED" {
		t.Fatal("All() did not return a defensive copy; package registry was mutated")
	}
}

// TestAllAndGetDeepCopySteps proves All() and Get() deep-copy the nested
// Steps slice, not just the top-level Template slice. Mutating a step through
// a returned template must not corrupt the package-level registry.
func TestAllAndGetDeepCopySteps(t *testing.T) {
	original := All()
	if len(original) == 0 || len(original[0].Steps) == 0 {
		t.Fatal("expected at least one template with at least one step")
	}
	wantName := original[0].Steps[0].Name

	// Mutate via All().
	got := All()
	got[0].Steps[0].Name = "PWNED-ALL"

	// Mutate via Get() (find a known key).
	key := original[0].Key
	tpl, ok := Get(key)
	if !ok {
		t.Fatalf("expected Get(%q) to succeed", key)
	}
	tpl.Steps[0].Name = "PWNED-GET"

	// Re-read the registry — neither mutation must have leaked.
	after := All()
	if after[0].Steps[0].Name != wantName {
		t.Fatalf("All()/Get() did not deep-copy Steps: step name mutated from %q to %q", wantName, after[0].Steps[0].Name)
	}
	got2, _ := Get(key)
	if got2.Steps[0].Name != wantName {
		t.Fatalf("Get() did not deep-copy Steps: step name mutated from %q to %q", wantName, got2.Steps[0].Name)
	}
}

// TestGetUnknownKey returns false for an unregistered key.
func TestGetUnknownKey(t *testing.T) {
	if _, ok := Get("does-not-exist"); ok {
		t.Fatal("expected ok=false for unknown key")
	}
}

// TestValidateRejectsMalformed ensures Validate guards every required field.
func TestValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		tpl  Template
		want string
	}{
		{"missing key", Template{Name: "x", Steps: []WorkflowStep{{Name: "s", Prompt: "p"}}}, "key is required"},
		{"missing name", Template{Key: "k", Steps: []WorkflowStep{{Name: "s", Prompt: "p"}}}, "name is required"},
		{"no steps", Template{Key: "k", Name: "x"}, "at least one step"},
		{"empty step name", Template{Key: "k", Name: "x", Steps: []WorkflowStep{{Prompt: "p"}}}, "missing name"},
		{"empty step prompt", Template{Key: "k", Name: "x", Steps: []WorkflowStep{{Name: "s"}}}, "missing prompt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.tpl.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected error containing %q, got %q", c.want, err.Error())
			}
		})
	}
}
