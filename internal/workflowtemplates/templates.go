// Package workflowtemplates provides predefined, instantiable workflow
// definitions that can be surfaced to users and turned into runnable
// workflows via the existing workflows API (POST /api/workflows).
//
// A Template is an opinionated, ordered set of WorkflowSteps. Templates are
// pure data — they carry no DB state of their own — so they can be listed and
// previewed before a user decides to instantiate one.
//
// The first concrete template is the SEO production workflow requested in
// issue #133 ("FEAT: SEO production workflows").
package workflowtemplates

import "fmt"

// WorkflowStep mirrors api.WorkflowStep so a Template can be persisted
// verbatim through the workflows create endpoint without translation.
type WorkflowStep struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// Template is a predefined, instantiable workflow definition.
type Template struct {
	// Key is the stable, machine-readable identifier for the template
	// (e.g. "seo-production"). It is unique across all templates.
	Key string `json:"key"`
	// Name is the human-readable title shown in surfaces.
	Name string `json:"name"`
	// Description summarises what the workflow produces.
	Description string `json:"description"`
	// Category groups related templates in surfaces (e.g. "content", "ops").
	Category string `json:"category"`
	// Steps is the ordered list of workflow steps.
	Steps []WorkflowStep `json:"steps"`
}

// Validate reports an error if the template is malformed: it must have a
// non-empty key, name, and at least one step, and every step must have a
// non-empty name and prompt.
func (t Template) Validate() error {
	if t.Key == "" {
		return fmt.Errorf("workflow template: key is required")
	}
	if t.Name == "" {
		return fmt.Errorf("workflow template %q: name is required", t.Key)
	}
	if len(t.Steps) == 0 {
		return fmt.Errorf("workflow template %q: at least one step is required", t.Key)
	}
	for i, s := range t.Steps {
		if s.Name == "" {
			return fmt.Errorf("workflow template %q: step %d missing name", t.Key, i)
		}
		if s.Prompt == "" {
			return fmt.Errorf("workflow template %q: step %d (%s) missing prompt", t.Key, i, s.Name)
		}
	}
	return nil
}

// seoProduction is the SEO content production workflow. It walks a topic from
// keyword/intent research through to publication-ready metadata, with each step
// feeding its output forward as context for the next.
var seoProduction = Template{
	Key:         "seo-production",
	Name:        "SEO Content Production",
	Description: "Produces a search-optimized, publication-ready article from a target topic or keyword — from intent research through on-page optimization and structured-data metadata.",
	Category:    "content",
	Steps: []WorkflowStep{
		{
			Name: "Keyword & Intent Research",
			Prompt: `You are an SEO strategist. Given the target topic or keyword, identify:
- The primary keyword and 5-8 semantically related secondary keywords.
- The dominant search intent (informational, commercial, transactional, navigational).
- The top-ranking content types and formats for this query (listicle, how-to, comparison, guide).
- 3-5 "people also ask" style questions the article should answer.

Return a concise, structured brief.`,
		},
		{
			Name: "SERP-Informed Outline",
			Prompt: `Using the keyword research from the previous step, produce a detailed article outline that:
- Targets the primary keyword in an H1 and structures H2/H3 subheadings around the secondary keywords and intent.
- Covers the "people also ask" questions identified earlier.
- Includes a logical content hierarchy designed to win a featured snippet where appropriate.
Return the outline as a nested heading structure.`,
		},
		{
			Name: "First Draft",
			Prompt: `Write a complete first draft of the article following the outline from the previous step. Requirements:
- Use the primary keyword naturally in the introduction, at least one H2, and the conclusion.
- Distribute secondary keywords across subheadings and body copy without keyword stuffing.
- Aim for an engaging, scannable tone with short paragraphs.
- Include a compelling meta-description-length summary as the opening.
Write the full body content.`,
		},
		{
			Name: "On-Page SEO Optimization",
			Prompt: `Review the draft from the previous step and apply on-page SEO refinements:
- Tighten title tag (<=60 chars) to include the primary keyword.
- Rewrite the meta description (<=155 chars) with the primary keyword and a call to action.
- Verify keyword density and placement, internal-link anchor suggestions, and image alt-text recommendations.
- Flag any thin sections or missing intent coverage.
Return the optimized sections and a short checklist of manual follow-ups.`,
		},
		{
			Name: "Metadata & Structured Data",
			Prompt: `Produce publication-ready metadata for the optimized article:
- A final title tag and meta description.
- A JSON-LD Article (or HowTo/FAQ where intent warrants) schema block.
- An Open Graph title, description, and suggested image alt text.
- A canonical URL slug recommendation derived from the primary keyword.
Return the metadata as clearly labelled blocks ready to paste into a CMS.`,
		},
	},
}

// all is the registry of every shipped template. Templates are validated at
// package init so a malformed definition fails fast at startup rather than at
// the first user request.
var all = []Template{
	seoProduction,
}

func init() {
	for _, t := range all {
		if err := t.Validate(); err != nil {
			panic(err)
		}
	}
}

// All returns every registered workflow template. The returned slice is a copy
// of the registry so callers cannot mutate the package-level definitions —
// including the nested Steps slice on each template, which is deep-copied so
// mutating a step through a returned template cannot corrupt the registry.
func All() []Template {
	out := make([]Template, len(all))
	copy(out, all)
	// copy() above only duplicates the top-level Template slice headers; the
	// backing arrays for each Steps slice would otherwise be shared with the
	// package-level registry, so deep-copy them too.
	for i := range out {
		out[i].Steps = append([]WorkflowStep(nil), out[i].Steps...)
	}
	return out
}

// Get returns the template with the given key and true if found. The returned
// template's Steps slice is a deep copy, so callers cannot mutate the
// package-level definitions through it.
func Get(key string) (Template, bool) {
	for _, t := range all {
		if t.Key == key {
			// The range value copies the Template struct, but its Steps slice
			// header still shares the registry's backing array — deep-copy it.
			t.Steps = append([]WorkflowStep(nil), t.Steps...)
			return t, true
		}
	}
	return Template{}, false
}
