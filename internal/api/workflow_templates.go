package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/workflowtemplates"
)

// WorkflowTemplateRoutes returns a Chi router exposing the catalog of
// predefined, instantiable workflow templates (the "concrete surface" for
// templated workflows such as SEO content production).
func (a *API) WorkflowTemplateRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListWorkflowTemplates)
	r.Get("/{key}", a.GetWorkflowTemplate)
	return r
}

// ListWorkflowTemplates handles GET /api/workflow-templates
func (a *API) ListWorkflowTemplates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workflowtemplates.All())
}

// GetWorkflowTemplate handles GET /api/workflow-templates/{key}
func (a *API) GetWorkflowTemplate(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	tpl, ok := workflowtemplates.Get(key)
	if !ok {
		http.Error(w, "workflow template not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tpl)
}
