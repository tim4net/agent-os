package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// WP-O3: Ledger API handler (run_log + findings)
// ---------------------------------------------------------------------------

// RunLogResponse is a single run_log record in the API response.
type RunLogResponse struct {
	ID        int64             `json:"id"`
	Ts        string            `json:"ts"`
	EventType string            `json:"event_type"`
	PrRef     string            `json:"pr_ref"`
	WpRef     string            `json:"wp_ref"`
	Summary   string            `json:"summary,omitempty"`
	Payload   json.RawMessage   `json:"payload,omitempty"`
}

// RunLogListResponse is the paginated response for GET /api/ledger/runs.
type RunLogListResponse struct {
	Records []RunLogResponse `json:"records"`
	Total   int64            `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// FindingResponse is a single findings record in the API response.
type FindingResponse struct {
	ID          int64  `json:"id"`
	Ts          string `json:"ts"`
	PrRef       string `json:"pr_ref"`
	WpRef       string `json:"wp_ref"`
	Gate        int32  `json:"gate"`
	AuthorAgent string `json:"author_agent"`
	Model       string `json:"model"`
	Severity    string `json:"severity"`
	Class       string `json:"class"`
	RootCause   string `json:"root_cause,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// FindingsListResponse is the paginated response for GET /api/ledger/findings.
type FindingsListResponse struct {
	Records []FindingResponse `json:"records"`
	Total   int64             `json:"total"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
}

// RecurringFindingsResponse is the response for GET /api/ledger/recurrence.
type RecurringFindingsResponse struct {
	Records []db.RecurringFindingsRow `json:"records"`
}

// PostRunLogRequest is the POST body for /api/ledger/runs.
type PostRunLogRequest struct {
	EventType string          `json:"event_type"`
	PrRef     string          `json:"pr_ref"`
	WpRef     string          `json:"wp_ref"`
	Summary   string          `json:"summary"`
	Payload   json.RawMessage `json:"payload"`
}

// PostFindingRequest is the POST body for /api/ledger/findings.
type PostFindingRequest struct {
	PrRef       string `json:"pr_ref"`
	WpRef       string `json:"wp_ref"`
	Gate        int32  `json:"gate"`
	AuthorAgent string `json:"author_agent"`
	Model       string `json:"model"`
	Severity    string `json:"severity"`
	Class       string `json:"class"`
	RootCause   string `json:"root_cause"`
	Summary     string `json:"summary"`
}

// LedgerRoutes returns a Chi router for ledger endpoints (WP-O3).
// Mounted at /api/ledger by the integrator (router.go).
func (a *API) LedgerRoutes() http.Handler {
	r := chi.NewRouter()
	r.Post("/runs", a.PostRunLog)
	r.Get("/runs", a.ListRunLog)
	r.Post("/findings", a.PostFinding)
	r.Get("/findings", a.ListFindings)
	r.Get("/recurrence", a.GetRecurringFindings)
	return r
}

// parsePagination extracts limit and offset from query parameters.
// Returns defaults (50, 0) if not provided. Returns 400 on bad values.
func parsePagination(w http.ResponseWriter, r *http.Request) (limit int, offset int, ok bool) {
	limit = 50
	offset = 0
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.ParseInt(l, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return 0, 0, false
		}
		if n > 10_000 {
			n = 10_000
		}
		limit = int(n)
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		n, err := strconv.ParseInt(o, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return 0, 0, false
		}
		if n > 1_000_000_000 {
			n = 1_000_000_000
		}
		offset = int(n)
	}
	return limit, offset, true
}

// textPG converts a string to pgtype.Text.
func textPG(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// PostRunLog handles POST /api/ledger/runs.
func (a *API) PostRunLog(w http.ResponseWriter, r *http.Request) {
	var req PostRunLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.EventType == "" {
		writeError(w, http.StatusBadRequest, "event_type is required")
		return
	}

	// Validate payload bytes; nil/empty → null column
	var payloadBytes []byte
	if len(req.Payload) > 0 {
		if !json.Valid(req.Payload) {
			writeError(w, http.StatusBadRequest, "payload must be valid JSON")
			return
		}
		payloadBytes = req.Payload
	}

	record, err := a.queries.AppendRunLog(r.Context(), db.AppendRunLogParams{
		EventType: req.EventType,
		PrRef:     req.PrRef,
		WpRef:     req.WpRef,
		Summary:   textPG(req.Summary),
		Payload:   payloadBytes,
	})
	if err != nil {
		slog.Default().Error("ledger: append run_log failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to append run_log")
		return
	}

	writeJSON(w, http.StatusOK, runLogToResponse(record))
}

// ListRunLog handles GET /api/ledger/runs?limit=50&offset=0
func (a *API) ListRunLog(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	records, err := a.queries.ListRunLog(r.Context(), db.ListRunLogParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		slog.Default().Error("ledger: list run_log failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list run_log")
		return
	}

	total, err := a.queries.CountRunLog(r.Context())
	if err != nil {
		slog.Default().Error("ledger: count run_log failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count run_log")
		return
	}

	resp := RunLogListResponse{
		Records: make([]RunLogResponse, 0, len(records)),
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	}
	for _, rec := range records {
		resp.Records = append(resp.Records, runLogToResponse(rec))
	}

	writeJSON(w, http.StatusOK, resp)
}

// PostFinding handles POST /api/ledger/findings.
func (a *API) PostFinding(w http.ResponseWriter, r *http.Request) {
	var req PostFindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Class == "" {
		writeError(w, http.StatusBadRequest, "class is required")
		return
	}

	record, err := a.queries.AppendFinding(r.Context(), db.AppendFindingParams{
		PrRef:       req.PrRef,
		WpRef:       req.WpRef,
		Gate:        req.Gate,
		AuthorAgent: req.AuthorAgent,
		Model:       req.Model,
		Severity:    req.Severity,
		Class:       req.Class,
		RootCause:   textPG(req.RootCause),
		Summary:     textPG(req.Summary),
	})
	if err != nil {
		slog.Default().Error("ledger: append finding failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to append finding")
		return
	}

	writeJSON(w, http.StatusOK, findingToResponse(record))
}

// ListFindings handles GET /api/ledger/findings?class=&severity=&wp_ref=&limit=50&offset=0
// Filter precedence: only one filter is applied per request (class > severity > wp_ref).
// If multiple filters are provided, the highest-priority non-empty filter wins;
// the others are silently ignored. This is a documented contract — callers must
// issue separate requests for combinations.
func (a *API) ListFindings(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	classFilter := r.URL.Query().Get("class")
	sevFilter := r.URL.Query().Get("severity")
	wpRefFilter := r.URL.Query().Get("wp_ref")

	var records []db.Finding
	var total int64
	var err error

	// Only one filter at a time; if multiple provided, class wins, then severity, then wp_ref.
	switch {
	case classFilter != "":
		records, err = a.queries.ListFindingsByClass(r.Context(), db.ListFindingsByClassParams{
			Class:  classFilter,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			slog.Default().Error("ledger: list findings by class failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list findings")
			return
		}
		total, err = a.queries.CountFindingsByClass(r.Context(), classFilter)
	case sevFilter != "":
		records, err = a.queries.ListFindingsBySeverity(r.Context(), db.ListFindingsBySeverityParams{
			Severity: sevFilter,
			Limit:    int32(limit),
			Offset:   int32(offset),
		})
		if err != nil {
			slog.Default().Error("ledger: list findings by severity failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list findings")
			return
		}
		total, err = a.queries.CountFindingsBySeverity(r.Context(), sevFilter)
	case wpRefFilter != "":
		records, err = a.queries.ListFindingsByWpRef(r.Context(), db.ListFindingsByWpRefParams{
			WpRef:  wpRefFilter,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			slog.Default().Error("ledger: list findings by wp_ref failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list findings")
			return
		}
		total, err = a.queries.CountFindingsByWpRef(r.Context(), wpRefFilter)
	default:
		records, err = a.queries.ListFindings(r.Context(), db.ListFindingsParams{
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			slog.Default().Error("ledger: list findings failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list findings")
			return
		}
		total, err = a.queries.CountFindings(r.Context())
	}

	if err != nil {
		slog.Default().Error("ledger: count findings failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count findings")
		return
	}

	resp := FindingsListResponse{
		Records: make([]FindingResponse, 0, len(records)),
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	}
	for _, rec := range records {
		resp.Records = append(resp.Records, findingToResponse(rec))
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetRecurringFindings handles GET /api/ledger/recurrence?min_count=3
func (a *API) GetRecurringFindings(w http.ResponseWriter, r *http.Request) {
	minCount := int64(3)
	if mc := r.URL.Query().Get("min_count"); mc != "" {
		n, err := strconv.ParseInt(mc, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "min_count must be a positive integer")
			return
		}
		minCount = n
	}

	records, err := a.queries.RecurringFindings(r.Context(), minCount)
	if err != nil {
		slog.Default().Error("ledger: recurring findings failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get recurring findings")
		return
	}

	writeJSON(w, http.StatusOK, RecurringFindingsResponse{Records: records})
}

// runLogToResponse converts a db.RunLog row to a JSON response struct.
// Payload is passed through as json.RawMessage — lossless, no round-trip.
func runLogToResponse(rec db.RunLog) RunLogResponse {
	resp := RunLogResponse{
		ID:        rec.ID,
		Ts:        rec.Ts.Time.UTC().Format("2006-01-02T15:04:05Z"),
		EventType: rec.EventType,
		PrRef:     rec.PrRef,
		WpRef:     rec.WpRef,
	}
	if rec.Summary.Valid {
		resp.Summary = rec.Summary.String
	}
	if len(rec.Payload) > 0 && json.Valid(rec.Payload) {
		resp.Payload = rec.Payload
	}
	return resp
}

// findingToResponse converts a db.Finding row to a JSON response struct.
func findingToResponse(rec db.Finding) FindingResponse {
	resp := FindingResponse{
		ID:          rec.ID,
		Ts:          rec.Ts.Time.UTC().Format("2006-01-02T15:04:05Z"),
		PrRef:       rec.PrRef,
		WpRef:       rec.WpRef,
		Gate:        rec.Gate,
		AuthorAgent: rec.AuthorAgent,
		Model:       rec.Model,
		Severity:    rec.Severity,
		Class:       rec.Class,
	}
	if rec.RootCause.Valid {
		resp.RootCause = rec.RootCause.String
	}
	if rec.Summary.Valid {
		resp.Summary = rec.Summary.String
	}
	return resp
}
