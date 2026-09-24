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

// An OpenAI-speaking agent using Claude through the gateway: its tools reach
// Anthropic in Anthropic's shape, what a tool returned is scanned like the
// rest of the prompt — here an email in a file the agent read is redacted
// before it leaves — and the model's tool call comes back as an OpenAI one.
func TestClaudeToolUseThroughChatCompletions(t *testing.T) {
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5",
			"content":[{"type":"tool_use","id":"toolu_2","name":"read_file","input":{"path":"b.txt"}}],
			"stop_reason":"tool_use","usage":{"input_tokens":50,"output_tokens":10}}`))
	}))
	defer upstream.Close()

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	ks.SetProviderKeys(context.Background(), storage.DefaultOrgID, map[string]string{"anthropic": "sk-ant-upstream"})
	h := Routes(Options{
		KeyStore: ks, DLPStore: storage.NewMemDLPStore(10),
		Inspector: inspector.New(), DLPPolicy: inspector.DefaultPolicy(),
		AnthropicBaseURL: upstream.URL, AdminAPIKey: "admin-test", Logger: slog.Default(),
	})
	body := `{"model":"claude-sonnet-4-5",
		"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}}],
		"messages":[
		 {"role":"user","content":"Summarise a.txt"},
		 {"role":"assistant","content":null,"tool_calls":[{"id":"toolu_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}]},
		 {"role":"tool","tool_call_id":"toolu_1","content":"Owner: ivan.petrov@corp.io. See b.txt."}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	b, _ := json.Marshal(sent)
	for _, want := range []string{`"input_schema"`, `"type":"tool_use"`, `"id":"toolu_1"`, `"type":"tool_result"`, `"tool_use_id":"toolu_1"`, `[REDACTED:email]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Anthropic received no %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "ivan.petrov@corp.io") {
		t.Error("the email in the tool result left the gateway")
	}

	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID       string
					Function struct{ Name, Arguments string }
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Choices) != 1 || resp.Choices[0].FinishReason != "tool_calls" || len(resp.Choices[0].Message.ToolCalls) != 1 ||
		resp.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"path":"b.txt"}` {
		t.Errorf("the agent got back: %s", rec.Body.String())
	}
}
