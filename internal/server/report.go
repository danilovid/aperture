package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/report"
	"github.com/mutegate/mutegate/internal/storage"
)

// handleDLPReport answers "what changes if we enable block" for a period:
// per-group impact, the rules and keys behind it, and the policies that let
// the traffic through. This is the report a team reads after a week in
// alert-only mode, before flipping a detector to block.
func (h *Handlers) handleDLPReport(w http.ResponseWriter, r *http.Request) {
	// The report is the one place a lost filter hides best: it only shows
	// totals, so a leak would look like a busy week rather than a bug.
	orgID, ok := h.adminOrg(w, r, storage.RoleViewer)
	if !ok {
		return
	}
	if h.DLPStore == nil {
		http.Error(w, `{"error":"dlp disabled"}`, http.StatusServiceUnavailable)
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		// The adoption question is about a week of traffic, not a day.
		period = "7d"
		q := r.URL.Query()
		q.Set("period", period)
		r.URL.RawQuery = q.Encode()
	}
	since := sinceParam(r)

	buckets, err := h.DLPStore.Aggregate(r.Context(), orgID, since)
	if err != nil {
		h.Logger.Error("dlp report aggregate failed", "err", err)
		http.Error(w, `{"error":"failed to build report"}`, http.StatusInternalServerError)
		return
	}
	totals, err := h.DLPStore.Summary(r.Context(), orgID, since)
	if err != nil {
		h.Logger.Error("dlp report summary failed", "err", err)
		http.Error(w, `{"error":"failed to build report"}`, http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	rep := report.Build(buckets, report.Options{
		Period:        period,
		Since:         since,
		Until:         time.Now(),
		Totals:        totals,
		DefaultPolicy: h.defaultPolicy(ctx, orgID),
		PolicyFor:     func(keyID string) inspector.Policy { return h.policyFor(ctx, orgID, keyID) },
		Truncated:     len(buckets) >= storage.MaxDLPBuckets,
	})
	if rep.Truncated {
		h.Logger.Warn("dlp report truncated — tables understate the tail",
			"buckets", len(buckets), "cap", storage.MaxDLPBuckets)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rep)
}

// defaultPolicy is what a key without its own binding gets.
func (h *Handlers) defaultPolicy(ctx context.Context, orgID string) inspector.Policy {
	if h.PolicyStore == nil {
		return h.DLPPolicy
	}
	p, err := h.PolicyStore.GetDefaultPolicy(ctx, orgID)
	if err != nil {
		h.Logger.Error("default policy lookup failed, using env fallback", "err", err)
		return h.DLPPolicy
	}
	return p
}
