package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danilovid/mutegate/internal/config"
	"github.com/danilovid/mutegate/internal/inspector"
	"github.com/danilovid/mutegate/internal/limits"
	"github.com/danilovid/mutegate/internal/storage"
)

// A Claude model called through the OpenAI chat API, without streaming, is
// metered like any other call: the tokens and the cost land on the usage row,
// and the spend counts against the key's budget. It used to be logged as free,
// so a budget never stopped it.
func TestClaudeThroughChatCompletionsIsMetered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		// claude-3-5-haiku input costs $0.80 per 1M tokens, so 625k tokens is $0.50.
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-haiku",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":625000,"output_tokens":0}}`))
	}))
	defer upstream.Close()

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), storage.DefaultOrgID, map[string]string{"anthropic": "sk-ant-upstream"}); err != nil {
		t.Fatal(err)
	}
	logs := &fakeLogStore{}
	ls := storage.NewMemLimitStore(limits.Limits{})
	ls.SetLimits(context.Background(), storage.DefaultOrgID, "runtime", limits.Limits{BudgetDailyUSD: 1.0})
	h := Routes(Options{
		KeyStore:         ks,
		LogStore:         logs,
		DLPStore:         storage.NewMemDLPStore(10),
		PolicyStore:      storage.NewMemPolicyStore(inspector.DefaultPolicy()),
		LimitStore:       ls,
		Tracker:          limits.NewTracker(nil),
		Inspector:        inspector.New(),
		DLPPolicy:        inspector.DefaultPolicy(),
		AnthropicBaseURL: upstream.URL,
		AdminAPIKey:      "admin-test",
		Logger:           slog.Default(),
	})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"claude-3-5-haiku","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ap-test")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := call()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Usage.PromptTokens != 625000 {
		t.Errorf("the client saw prompt_tokens = %d, want 625000", resp.Usage.PromptTokens)
	}
	if len(logs.entries) != 1 {
		t.Fatalf("want 1 usage row, got %d", len(logs.entries))
	}
	if e := logs.entries[0]; e.Provider != "anthropic" || e.PromptTokens != 625000 || math.Abs(e.CostUSD-0.50) > 1e-9 {
		t.Errorf("usage row = provider %s, %d tokens, $%v; want anthropic, 625000, $0.50",
			e.Provider, e.PromptTokens, e.CostUSD)
	}

	// $0.50 spent, then $1.00: the budget is used up and the next call is refused.
	if rec := call(); rec.Code != http.StatusOK {
		t.Fatalf("second call: status = %d, want 200", rec.Code)
	}
	if rec := call(); rec.Code != http.StatusTooManyRequests {
		t.Errorf("third call: status = %d, want 429 once $1.00 is spent", rec.Code)
	}
}
