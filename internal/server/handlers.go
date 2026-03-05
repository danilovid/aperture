package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danilovid/aperture/internal/provider"
	"github.com/danilovid/aperture/internal/provider/openai"
	"github.com/danilovid/aperture/internal/storage"
)

// Handlers holds dependencies for API handlers.
type Handlers struct {
	KeyStore      storage.KeyStore
	OpenAIBaseURL string
	AdminAPIKey   string
	Logger        *slog.Logger
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(prefix):])
}

func (h *Handlers) resolveProvider(r *http.Request) (provider.Provider, error) {
	token := extractBearerToken(r)
	if token == "" {
		return nil, storage.ErrKeyNotFound
	}
	key, err := h.KeyStore.GetByApertureKey(r.Context(), token)
	if err != nil {
		return nil, err
	}
	return openai.New(h.OpenAIBaseURL, key.OpenAIAPIKey), nil
}

func (h *Handlers) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *Handlers) handleReady(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

func (h *Handlers) handleModels(w http.ResponseWriter, r *http.Request) {
	p, err := h.resolveProvider(r)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}
	body, contentType, status, err := p.Models(r.Context())
	if err != nil {
		h.Logger.Error("models request failed", "err", err)
		http.Error(w, `{"error":"failed to fetch models"}`, http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	io.Copy(w, body)
}

func (h *Handlers) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	p, err := h.resolveProvider(r)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	body, respContentType, status, err := p.ChatCompletions(r.Context(), r.Body, contentType)
	if err != nil {
		h.Logger.Error("chat completions request failed", "err", err)
		http.Error(w, `{"error":"failed to proxy request"}`, http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", respContentType)
	w.WriteHeader(status)

	if flusher, ok := w.(http.Flusher); ok && isStreaming(respContentType) {
		buf := make([]byte, 32*1024)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				flusher.Flush()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
		}
	} else {
		io.Copy(w, body)
	}
}

func (h *Handlers) writeAuthError(w http.ResponseWriter, err error) {
	if err == storage.ErrKeyNotFound {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid or missing API key"})
		return
	}
	h.Logger.Error("key lookup failed", "err", err)
	http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
}

// --- Admin handlers ---

func (h *Handlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if h.AdminAPIKey == "" {
		http.Error(w, `{"error":"admin API disabled"}`, http.StatusForbidden)
		return false
	}
	token := extractBearerToken(r)
	if token != h.AdminAPIKey {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid admin key"})
		return false
	}
	return true
}

func (h *Handlers) handleAdminCreateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	var req struct {
		ApertureKey  string `json:"aperture_key"`
		OpenAIAPIKey string `json:"openai_api_key"`
		Name         string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.ApertureKey == "" || req.OpenAIAPIKey == "" {
		http.Error(w, `{"error":"aperture_key and openai_api_key required"}`, http.StatusBadRequest)
		return
	}
	key, err := h.KeyStore.Create(r.Context(), req.ApertureKey, req.OpenAIAPIKey, req.Name)
	if err != nil {
		h.Logger.Error("create key failed", "err", err)
		http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id":          key.ID,
		"aperture_key": key.ApertureKey,
		"name":        key.Name,
		"created_at":  key.CreatedAt,
	})
}

func (h *Handlers) handleAdminListKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	keys, err := h.KeyStore.List(r.Context())
	if err != nil {
		h.Logger.Error("list keys failed", "err", err)
		http.Error(w, `{"error":"failed to list keys"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

func (h *Handlers) handleAdminDeleteKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"key id required"}`, http.StatusBadRequest)
		return
	}
	if err := h.KeyStore.Delete(r.Context(), id); err != nil {
		if err == storage.ErrKeyNotFound {
			http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
			return
		}
		h.Logger.Error("delete key failed", "err", err)
		http.Error(w, `{"error":"failed to delete key"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isStreaming(contentType string) bool {
	return strings.HasPrefix(contentType, "text/event-stream")
}
