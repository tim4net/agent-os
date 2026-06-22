# SEO Production Workflows

> Issue #133 — FEAT: SEO production workflows

This document describes the **SEO Content Production** workflow template, the
first in a catalog of predefined, instantiable workflows shipped with
Agent OS. It is intentionally low-priority / domain-specific and is deferred
behind higher-priority platform work.

## Design

SEO production is delivered as a **workflow template** — a pure-data,
ordered set of steps that lives in the `internal/workflowtemplates` package
and is surfaced through a read-only API. A user instantiates a template into
a runnable workflow via the existing `POST /api/workflows` endpoint; nothing
DB-side is specific to SEO.

This keeps the surface small (no new tables, no migrations) while giving
"Goldie" a concrete, one-click starting point for SEO content production.

## The `seo-production` template

| Step | Purpose |
|------|---------|
| 1. Keyword & Intent Research | Primary/secondary keywords, search intent, content-type, PAA questions. |
| 2. SERP-Informed Outline | H1/H2/H3 hierarchy built from the keyword research, snippet-optimized. |
| 3. First Draft | Full article body using the outline; natural keyword placement. |
| 4. On-Page SEO Optimization | Title tag (≤60), meta description (≤155), density, alt-text, intent gaps. |
| 5. Metadata & Structured Data | Final title/meta, JSON-LD schema, Open Graph, canonical slug. |

Each step feeds its output forward as context for the next, exactly like any
other workflow run (see `RunWorkflow` in `internal/api/workflows.go`).

## API surface

```
GET /api/workflow-templates          → list all templates
GET /api/workflow-templates/{key}    → fetch a single template by key
```

Example:

```bash
curl -s /api/workflow-templates/seo-production | jq '.name, (.steps | length)'
"SEO Content Production"
5
```

To instantiate, post the template's `name`, `description`, and `steps` to the
existing create endpoint:

```bash
curl -s /api/workflow-templates/seo-production \
  | jq '{name, description, steps}' \
  | curl -s -X POST /api/workflows -d @- -H 'Content-Type: application/json'
```

The returned workflow can then be run with `POST /api/workflows/{id}/run`.

## Frontend surface

The **Workflows** view renders a **Templates** panel above the workflow grid.
Clicking **Use Template** creates a runnable workflow from the SEO template
and refreshes the list.

## Adding more templates

Templates are defined in `internal/workflowtemplates/templates.go`. Append a
new `Template` to the `all` slice. Every template is validated at package
`init()` (non-empty key/name, ≥1 step, every step has a name and prompt), so a
malformed definition fails fast at startup rather than at request time.

```go
var myTemplate = Template{
    Key:      "my-workflow",
    Name:     "My Workflow",
    Category: "content",
    Steps:    []WorkflowStep{{Name: "...", Prompt: "..."}},
}

var all = []Template{seoProduction, myTemplate}
```
