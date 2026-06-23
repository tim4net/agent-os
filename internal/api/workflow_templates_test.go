package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tim4net/agent-os/internal/workflowtemplates"
)

// newTemplateRouter builds the real chi router for the templates surface so
// URL params (e.g. {key}) resolve through chi's route context.
func newTemplateRouter() http.Handler {
	return (&API{}).WorkflowTemplateRoutes()
}

// TestListWorkflowTemplates verifies the catalog surface returns the SEO
// production template and is well-formed JSON.
func TestListWorkflowTemplates(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	newTemplateRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", rec.Code, rec.Body.String())
	}

	var got []workflowtemplates.Template
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one template")
	}

	found := false
	for _, tpl := range got {
		if tpl.Key == "seo-production" {
			found = true
			if len(tpl.Steps) < 4 {
				t.Fatalf("seo-production has only %d steps", len(tpl.Steps))
			}
		}
	}
	if !found {
		t.Fatal("seo-production template missing from list")
	}
}

// TestGetWorkflowTemplate verifies a single template can be fetched by key.
func TestGetWorkflowTemplate(t *testing.T) {
	req := httptest.NewRequest("GET", "/seo-production", nil)
	rec := httptest.NewRecorder()
	newTemplateRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", rec.Code, rec.Body.String())
	}

	var tpl workflowtemplates.Template
	if err := json.Unmarshal(rec.Body.Bytes(), &tpl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tpl.Key != "seo-production" {
		t.Fatalf("expected key seo-production, got %q", tpl.Key)
	}
}

// TestGetWorkflowTemplateNotFound verifies an unknown key returns 404.
func TestGetWorkflowTemplateNotFound(t *testing.T) {
	req := httptest.NewRequest("GET", "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	newTemplateRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
