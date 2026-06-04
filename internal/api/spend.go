package api

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/db"
)

// epsilon for float64 cost comparisons.
const spendEpsilon = 1e-6

// feq returns true if a and b are within spendEpsilon of each other.
func feq(a, b float64) bool { return math.Abs(a-b) < spendEpsilon }

// SpendRow is a single row in the spend/usage aggregation response.
//
// Usage (TotalTokens, TotalTurns) is ALWAYS populated and is the primary metric.
// TotalCostUsd is a nullable pointer: nil means "no dollar cost applies" — either
// the group is a subscription (flat-rate) account or no session reported a cost.
// A non-nil 0 would mean "metered, but free"; nil means "cost not applicable."
type SpendRow struct {
	DimensionKey string   `json:"dimension_key"`
	TotalCostUsd *float64 `json:"total_cost_usd"`
	TotalTokens  int64    `json:"total_tokens"`
	TotalTurns   int64    `json:"total_turns"`
	SessionCount int64    `json:"session_count"`
	// BillingMode classifies the group: "subscription" | "metered" | "unknown".
	// Resolved from the provider map (Option A). Only authoritative when
	// group_by=agent (a single harness → single provider); for project/tenant/day
	// groups it is "unknown" because a group can span multiple providers.
	BillingMode string `json:"billing_mode"`
	// Provider is the resolved provider for agent-grouped rows ("" otherwise).
	Provider string `json:"provider"`
}

// SpendResponse is the API response for GET /api/spend.
type SpendResponse struct {
	Rows   []SpendRow `json:"rows"`
	Total  int64      `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// SpendRoutes returns a Chi router for spend endpoints.
func (a *API) SpendRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.GetSpend)
	return r
}

// Valid group_by values for the spend endpoint.
var validGroupBy = map[string]bool{
	"agent":   true,
	"project": true,
	"tenant":  true,
	"day":     true,
}

// GetSpend handles GET /api/spend?group_by=agent|project|tenant|day&tenant=...&external_ref=...&limit=50&offset=0
// Aggregates cost_usd + num_turns from work-events per the requested dimension.
// Pure read over existing work_events — no migration needed (WP-K).
// Per the work-event contract, cost_usd is cumulative per session; the query
// dedupes to the latest event per (harness, session_id) before rolling up.
func (a *API) GetSpend(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "agent" // default
	}
	if !validGroupBy[groupBy] {
		writeError(w, http.StatusBadRequest, "invalid group_by: must be agent|project|tenant|day")
		return
	}

	tenant := r.URL.Query().Get("tenant")
	externalRef := r.URL.Query().Get("external_ref")

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			if n > 10_000 {
				n = 10_000
			}
			limit = int(n)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.ParseInt(o, 10, 64); err == nil && n >= 0 {
			if n > 1_000_000_000 {
				n = 1_000_000_000
			}
			offset = int(n)
		}
	}

	rows, err := a.queries.AggregateSpend(r.Context(), db.AggregateSpendParams{
		GroupBy:     groupBy,
		Off:         int32(offset),
		Lim:         int32(limit),
		Tenant:      tenant,
		ExternalRef: externalRef,
	})
	if err != nil {
		slog.Default().Error("spend: aggregate failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to aggregate spend")
		return
	}

	// Convert pgtype.Numeric to *float64 for JSON (nil = cost not applicable).
	result := make([]SpendRow, 0, len(rows))
	var totalGroups int64
	for i, row := range rows {
		var costPtr *float64
		if row.TotalCostUsd.Valid {
			cost, err := row.TotalCostUsd.Float64Value()
			if err != nil {
				slog.Default().Error("spend: numeric convert failed", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to read spend total")
				return
			}
			if cost.Valid {
				c := cost.Float64
				costPtr = &c
			}
		}

		// Provider/billing mode resolves from the group's representative telemetry
		// model (most specific signal) plus harness. For agent groups the harness
		// is constant; the model refines it (e.g. harness=generic + model=gpt-* →
		// openai/metered). For project/tenant/day a group can span providers, but
		// a homogeneous group still classifies correctly; mixed groups fall back to
		// the dominant model captured by the query.
		harnessHint := ""
		if groupBy == "agent" {
			harnessHint = row.DimensionKey
		}
		provider := ResolveProvider(harnessHint, row.GroupModel)
		billingMode := string(BillingModeFor(provider))

		// For subscription/unknown groups, a dollar figure is not a real bill —
		// suppress it so the UI never presents fabricated cost as spend.
		if billingMode != string(BillingMetered) {
			costPtr = nil
		}

		result = append(result, SpendRow{
			DimensionKey: row.DimensionKey,
			TotalCostUsd: costPtr,
			TotalTokens:  row.TotalTokens,
			TotalTurns:   row.TotalTurns,
			SessionCount: row.SessionCount,
			BillingMode:  billingMode,
			Provider:     provider,
		})
		// Every row carries the same total_groups (window function); take from first.
		if i == 0 {
			totalGroups = row.TotalGroups
		}
	}

	// When offset ≥ group count, the main query returns zero rows and totalGroups
	// stays 0. Fall back to a dedicated count query for an accurate Total.
	if len(rows) == 0 {
		total, err := a.queries.CountSpendGroups(r.Context(), db.CountSpendGroupsParams{
			GroupBy:     groupBy,
			Tenant:      tenant,
			ExternalRef: externalRef,
		})
		if err != nil {
			slog.Default().Error("spend: count groups failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to count spend groups")
			return
		}
		totalGroups = total
	}

	writeJSON(w, http.StatusOK, SpendResponse{
		Rows:   result,
		Total:  totalGroups,
		Limit:  limit,
		Offset: offset,
	})
}
