package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danilovid/aperture/internal/storage"
)

func postChatAs(h http.Handler, content, agent, session string) *httptest.ResponseRecorder {
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + content + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	if agent != "" {
		req.Header.Set("X-Aperture-Agent", agent)
	}
	if session != "" {
		req.Header.Set("X-Aperture-Session", session)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The point of the feature: two agents sharing one key must be told apart.
func TestAttributionDistinguishesAgentsOnOneKey(t *testing.T) {
	h, dlp, _ := chatRouterWithDLP(t)

	postChatAs(h, "key AKIAIOSFODNN7EXAMPLE", "ci-bot", "run-1")
	postChatAs(h, "key AKIAIOSFODNN7EXAMPLE", "dev-ivan", "run-2")

	all, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(all) != 2 {
		t.Fatalf("want 2 events, got %d", len(all))
	}

	ci, _ := dlp.List(context.Background(), storage.DLPFilter{Agent: "ci-bot"})
	if len(ci) != 1 || ci[0].Session != "run-1" {
		t.Errorf("agent filter failed: %+v", ci)
	}
	dev, _ := dlp.List(context.Background(), storage.DLPFilter{Agent: "dev-ivan"})
	if len(dev) != 1 || dev[0].Session != "run-2" {
		t.Errorf("agent filter failed: %+v", dev)
	}
	bySession, _ := dlp.List(context.Background(), storage.DLPFilter{Session: "run-2"})
	if len(bySession) != 1 || bySession[0].Agent != "dev-ivan" {
		t.Errorf("session filter failed: %+v", bySession)
	}
}

// Attribution is optional; without the headers everything still works.
func TestAttributionIsOptional(t *testing.T) {
	h, dlp, _ := chatRouterWithDLP(t)
	postChatAs(h, "key AKIAIOSFODNN7EXAMPLE", "", "")

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Agent != "" || events[0].Session != "" {
		t.Errorf("unexpected attribution: %+v", events)
	}
}

// A caller must not be able to push unbounded header data into the store.
func TestAttributionValuesAreBounded(t *testing.T) {
	h, dlp, _ := chatRouterWithDLP(t)
	postChatAs(h, "key AKIAIOSFODNN7EXAMPLE", strings.Repeat("a", 500), "s")

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 {
		t.Fatalf("want 1 event")
	}
	if len(events[0].Agent) != maxAttrLen {
		t.Errorf("agent not truncated: %d chars", len(events[0].Agent))
	}
}

// Attribution also reaches the usage rows, so cost can be split per agent.
func TestAttributionReachesUsageLog(t *testing.T) {
	h, logs, _ := chatRouterWithLogs(t)
	postChatAs(h, "hello", "ci-bot", "run-9")

	if len(logs.entries) != 1 {
		t.Fatalf("want 1 usage row, got %d", len(logs.entries))
	}
	if e := logs.entries[0]; e.Agent != "ci-bot" || e.Session != "run-9" {
		t.Errorf("usage row missing attribution: agent=%q session=%q", e.Agent, e.Session)
	}
}

// The events API exposes the filters too.
func TestEventsAPIFiltersByAgent(t *testing.T) {
	h, _, _ := chatRouterWithDLP(t)
	postChatAs(h, "key AKIAIOSFODNN7EXAMPLE", "ci-bot", "run-1")
	postChatAs(h, "key AKIAIOSFODNN7EXAMPLE", "dev-ivan", "run-2")

	rec := adminReq(h, http.MethodGet, "/admin/dlp/events?agent=ci-bot", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ci-bot") || strings.Contains(rec.Body.String(), "dev-ivan") {
		t.Errorf("agent query filter not applied: %s", rec.Body.String())
	}
}
