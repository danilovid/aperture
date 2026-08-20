package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
)

// limitsRouter wires a gateway with limits enforcement and an upstream that
// reports usage, so spend accumulates as it would in production.
func limitsRouter(t *testing.T) (http.Handler, *storage.MemLimitStore, *storage.MemDLPStore, *int) {
	t.Helper()
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		// gpt-4o input costs $2.50 per 1M tokens, so 200k tokens is $0.50.
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":200000,"completion_tokens":0,"total_tokens":200000}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	ls := storage.NewMemLimitStore(limits.Limits{})
	dlp := storage.NewMemDLPStore(50)
	h := Routes(Options{
		KeyStore:      ks,
		DLPStore:      dlp,
		PolicyStore:   storage.NewMemPolicyStore(inspector.DefaultPolicy()),
		LimitStore:    ls,
		Tracker:       limits.NewTracker(nil),
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, ls, dlp, &upstreamHits
}

func chat(h http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The acceptance criterion: a key with a $1 daily budget starts receiving 429s
// once it has spent $1.
func TestBudgetReturns429AfterExhaustion(t *testing.T) {
	h, ls, dlp, hits := limitsRouter(t)
	ls.SetLimits(context.Background(), "runtime", limits.Limits{BudgetDailyUSD: 1.0})

	for i := 1; i <= 2; i++ { // $0.50 each
		if rec := chat(h); rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200", i, rec.Code)
		}
	}
	rec := chat(h)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body: %s", rec.Code, rec.Body.String())
	}
	if *hits != 2 {
		t.Errorf("over-budget request still reached the upstream: %d hits", *hits)
	}
	if ra, _ := strconv.Atoi(rec.Header().Get("Retry-After")); ra <= 0 {
		t.Errorf("missing Retry-After: %q", rec.Header().Get("Retry-After"))
	}

	var resp struct {
		Error struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
		Aperture struct {
			SpentUSD  float64 `json:"spent_usd"`
			BudgetUSD float64 `json:"budget_usd"`
		} `json:"aperture"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error.Type != "aperture_limit_exceeded" || resp.Error.Reason != "budget" {
		t.Errorf("unexpected error payload: %+v", resp.Error)
	}
	if resp.Aperture.SpentUSD < 1.0 || resp.Aperture.BudgetUSD != 1.0 {
		t.Errorf("spend detail wrong: %+v", resp.Aperture)
	}

	// The cut-off shows up in the incident feed once, not per rejected request.
	chat(h)
	events, _ := dlp.List(context.Background(), storage.DLPFilter{Rule: "budget-exceeded"})
	if len(events) != 1 {
		t.Errorf("want exactly 1 budget event, got %d", len(events))
	}
}

func TestRateLimitReturns429(t *testing.T) {
	h, ls, _, _ := limitsRouter(t)
	ls.SetLimits(context.Background(), "runtime", limits.Limits{RequestsPerMinute: 2})

	for i := 1; i <= 2; i++ {
		if rec := chat(h); rec.Code != http.StatusOK {
			t.Fatalf("call %d refused early: %d", i, rec.Code)
		}
	}
	rec := chat(h)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	var resp struct {
		Error struct{ Reason string } `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error.Reason != "rate" {
		t.Errorf("reason = %q, want rate", resp.Error.Reason)
	}
}

func TestNoLimitsConfiguredMeansNoEnforcement(t *testing.T) {
	h, _, _, _ := limitsRouter(t)
	for i := 0; i < 5; i++ {
		if rec := chat(h); rec.Code != http.StatusOK {
			t.Fatalf("call %d blocked with no limits set: %d", i, rec.Code)
		}
	}
}

func TestDefaultLimitsApplyToKeysWithoutTheirOwn(t *testing.T) {
	h, ls, _, _ := limitsRouter(t)
	ls.SetDefaultLimits(context.Background(), limits.Limits{RequestsPerMinute: 1})

	if rec := chat(h); rec.Code != http.StatusOK {
		t.Fatalf("first call refused: %d", rec.Code)
	}
	if rec := chat(h); rec.Code != http.StatusTooManyRequests {
		t.Errorf("default limits not applied: %d", rec.Code)
	}
}

func TestLimitsAdminAPI(t *testing.T) {
	h, _, _, _ := limitsRouter(t)

	if rec := adminReq(h, http.MethodPut, "/admin/limits/keys/runtime",
		`{"budget_daily_usd":5,"requests_per_minute":60}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Default limits.Limits            `json:"default"`
		Keys    map[string]limits.Limits `json:"keys"`
		Spent   map[string]float64       `json:"spent_usd"`
	}
	rec := adminReq(h, http.MethodGet, "/admin/limits", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Keys["runtime"].BudgetDailyUSD != 5 || got.Keys["runtime"].RequestsPerMinute != 60 {
		t.Errorf("limits not stored: %+v", got.Keys)
	}

	// Spend is reported next to the ceiling so the console can show usage.
	chat(h)
	rec = adminReq(h, http.MethodGet, "/admin/limits", "")
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Spent["runtime"] <= 0 {
		t.Errorf("spend not tracked: %v", got.Spent)
	}

	if rec := adminReq(h, http.MethodDelete, "/admin/limits/keys/runtime", ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE status = %d", rec.Code)
	}
}

func TestLimitsValidationAndAuth(t *testing.T) {
	h, _, _, _ := limitsRouter(t)
	if rec := adminReq(h, http.MethodPut, "/admin/limits/default",
		`{"budget_daily_usd":-1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("negative budget accepted: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/limits", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("limits API without admin key: %d, want 401", rec.Code)
	}
}
