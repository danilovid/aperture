package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danilovid/aperture/internal/config"
)

func TestHandleAdminCreateKey(t *testing.T) {
	t.Parallel()

	ks := config.NewRuntimeStore("dev").KeyStore()
	handler := Routes(
		ks,
		nil,
		"",
		"admin-secret",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	req := httptest.NewRequest(http.MethodPost, "/admin/keys", strings.NewReader(`{
		"aperture_key":"sk-aperture-1",
		"name":"team-a",
		"openai_api_key":"sk-openai-1"
	}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body=%s", rr.Code, http.StatusCreated, rr.Body.String())
	}

	key, err := ks.GetByApertureKey(context.Background(), "sk-aperture-1")
	if err != nil {
		t.Fatalf("GetByApertureKey() error = %v", err)
	}
	if got := key.Providers["openai"]; got != "sk-openai-1" {
		t.Fatalf("openai key = %q, want sk-openai-1", got)
	}
}
