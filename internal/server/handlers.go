package server

import (
	"encoding/json"
	"net/http"
)

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	// TODO: check downstream connections, etc.
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	// Placeholder: OpenAI /v1/models response shape
	resp := map[string]any{
		"object": "list",
		"data":   []any{},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Placeholder: will proxy to OpenAI
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{
		"error": "chat completions not implemented yet",
	})
}
