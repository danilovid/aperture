package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danilovid/aperture/internal/config"
)

func testRouter(adminKey string, origins []string) http.Handler {
	ks := config.NewRuntimeStore("ap-test").KeyStore()
	return Routes(ks, nil, "https://api.openai.example", adminKey, origins, slog.Default())
}

func TestAdminRequiresKey(t *testing.T) {
	h := testRouter("admin-secret", nil)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no auth", "", http.StatusUnauthorized},
		{"wrong key", "Bearer nope", http.StatusUnauthorized},
		{"valid key", "Bearer admin-secret", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestAdminFailsClosedWithEmptyKey(t *testing.T) {
	h := testRouter("", nil)
	for _, header := range []string{"", "Bearer anything"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("header %q: status = %d, want 401", header, rec.Code)
		}
	}
}

func TestChatCompletionsRejectsInvalidKey(t *testing.T) {
	h := testRouter("admin-secret", nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer dev")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCORSAllowlist(t *testing.T) {
	h := testRouter("admin-secret", []string{"http://localhost:5173"})

	req := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("allowed origin not reflected, got %q", got)
	}

	req = httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin got CORS header %q", got)
	}
}
