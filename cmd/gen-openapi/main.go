// Command gen-openapi generates an OpenAPI 3.1 specification by walking the
// live chi router. The routes are extracted from the actual registered router
// (internal/api.(*API).Router) so the spec can never silently drift from the
// code: regenerate with `go run ./cmd/gen-openapi > docs/openapi.yaml`.
//
// Path/operation metadata (summaries, tags) is enriched from the handler's
// reflected function name. This is intentionally schema-light: it documents the
// full route surface (method + path + handler) authoritatively, which is the
// part that drifts. Request/response bodies are referenced to the SQL schema in
// internal/db/migrations and the sqlc query layer, which remain the
// source of truth for payload shapes.
package main

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tim4net/agent-os/internal/api"
)

type route struct {
	Method  string
	Path    string
	Handler string
	Tag     string
	OpID    string
}

func main() {
	// Construct the API with zero-value dependencies. Every sub-API constructor
	// (NewArtifactAPI, NewMemoryAPI, NewStudioAPI) is a pure field-store /
	// provider-registration with no DB or network I/O, so route registration is
	// safe with nil deps. We only ever call Router(), never serve traffic.
	a := api.NewAPI(
		nil,                     // queries
		nil,                     // pool
		nil,                     // registry
		nil,                     // bus
		nil,                     // feed
		"",                      // litellmURL
		"",                      // artifactsPath
		"",                      // obsidianPath
		"",                      // hermesSkillsPath
		map[string]string{},     // apiKeys
		"", "", "", "",          // hermes/zai/openrouter keys, llmModel
	)

	// The router is mounted at /api in cmd/server/main.go; replicate that prefix
	// so the emitted paths match what clients actually call.
	root := chi.NewRouter()
	root.Mount("/api", a.Router())

	var routes []route
	err := chi.Walk(root, func(method, path string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi emits trailing-slash paths for sub-route roots; normalize.
		clean := path
		if len(clean) > 1 && strings.HasSuffix(clean, "/") {
			clean = strings.TrimRight(clean, "/")
		}
		hn := handlerName(handler)
		routes = append(routes, route{
			Method:  method,
			Path:    clean,
			Handler: hn,
			Tag:     tagFor(clean),
			OpID:    opID(method, clean, hn),
		})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk error:", err)
		os.Exit(1)
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})

	emit(routes)
}

// handlerName reflects the Go function name backing a chi route, e.g.
// "github.com/tim4net/agent-os/internal/api.(*API).DetailedHealth-fm" → "DetailedHealth".
func handlerName(h http.Handler) string {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Func {
		// chi wraps handlers; pull the ServeHTTP target if present.
		if hf, ok := h.(http.HandlerFunc); ok {
			v = reflect.ValueOf(hf)
		} else {
			return ""
		}
	}
	full := runtime.FuncForPC(v.Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	return full
}

// tagFor groups operations by their top-level /api/<segment> resource.
func tagFor(path string) string {
	p := strings.TrimPrefix(path, "/api/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	if p == "" || p == "/api" {
		return "root"
	}
	return p
}

func opID(method, path, handler string) string {
	if handler != "" {
		return strings.ToLower(method) + "_" + handler
	}
	slug := strings.ReplaceAll(strings.Trim(path, "/"), "/", "_")
	slug = strings.NewReplacer("{", "", "}", "").Replace(slug)
	return strings.ToLower(method) + "_" + slug
}

// emit writes an OpenAPI 3.1 document to stdout. Hand-rolled YAML keeps the
// generator dependency-free (no external OpenAPI library in go.mod).
func emit(routes []route) {
	// Collect path parameters from {brace} segments.
	w := os.Stdout
	fmt.Fprintln(w, "# GENERATED FILE — do not edit by hand.")
	fmt.Fprintln(w, "# Regenerate: go run ./cmd/gen-openapi > docs/openapi.yaml")
	fmt.Fprintln(w, "# Source of truth: internal/api.(*API).Router() walked via chi.Walk.")
	fmt.Fprintln(w, "openapi: 3.1.0")
	fmt.Fprintln(w, "info:")
	fmt.Fprintln(w, "  title: Agent OS API")
	fmt.Fprintln(w, "  version: 0.6.0")
	fmt.Fprintln(w, "  description: >-")
	fmt.Fprintln(w, "    Control plane and observability API for the Agent OS SPOG. Route")
	fmt.Fprintln(w, "    surface is generated authoritatively from the chi router; payload")
	fmt.Fprintln(w, "    shapes are defined by the SQL migrations (internal/db/migrations)")
	fmt.Fprintln(w, "    and the sqlc query layer (internal/db/queries).")
	fmt.Fprintln(w, "servers:")
	fmt.Fprintln(w, "  - url: http://localhost:8080")
	fmt.Fprintln(w, "    description: Local / hpms1 deployment")

	// Tags
	tagSet := map[string]bool{}
	for _, r := range routes {
		tagSet[r.Tag] = true
	}
	var tags []string
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	fmt.Fprintln(w, "tags:")
	for _, t := range tags {
		fmt.Fprintf(w, "  - name: %s\n", t)
	}

	// Group routes by path.
	byPath := map[string][]route{}
	var order []string
	for _, r := range routes {
		if _, ok := byPath[r.Path]; !ok {
			order = append(order, r.Path)
		}
		byPath[r.Path] = append(byPath[r.Path], r)
	}

	fmt.Fprintln(w, "paths:")
	for _, p := range order {
		fmt.Fprintf(w, "  %s:\n", p)
		params := pathParams(p)
		for _, r := range byPath[p] {
			fmt.Fprintf(w, "    %s:\n", strings.ToLower(r.Method))
			fmt.Fprintf(w, "      tags: [%s]\n", r.Tag)
			fmt.Fprintf(w, "      operationId: %s\n", r.OpID)
			summary := r.Handler
			if summary == "" {
				summary = r.Method + " " + r.Path
			}
			fmt.Fprintf(w, "      summary: %s\n", camelToWords(summary))
			fmt.Fprintf(w, "      description: Handler %s in internal/api.\n", orNone(r.Handler))
			if len(params) > 0 {
				fmt.Fprintln(w, "      parameters:")
				for _, pp := range params {
					fmt.Fprintf(w, "        - name: %s\n", pp)
					fmt.Fprintln(w, "          in: path")
					fmt.Fprintln(w, "          required: true")
					fmt.Fprintln(w, "          schema: { type: string }")
				}
			}
			fmt.Fprintln(w, "      responses:")
			fmt.Fprintln(w, "        '200': { description: Success }")
			fmt.Fprintln(w, "        '400': { description: Bad request }")
			fmt.Fprintln(w, "        '404': { description: Not found }")
			fmt.Fprintln(w, "        '500': { description: Server error }")
		}
	}
}

func pathParams(p string) []string {
	var out []string
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, strings.Trim(seg, "{}"))
		}
	}
	return out
}

func camelToWords(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(anonymous)"
	}
	return s
}
