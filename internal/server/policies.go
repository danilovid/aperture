package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/danilovid/aperture/internal/inspector"
)

// validatePolicy rejects unknown actions and non-compiling custom patterns.
func validatePolicy(p inspector.Policy) error {
	for name, a := range map[string]inspector.Action{
		"secrets": p.Secrets, "pii": p.PII, "custom": p.Custom,
	} {
		if !inspector.ValidAction(string(a)) {
			return fmt.Errorf("invalid action for %s: %q (want off|alert|redact|block)", name, a)
		}
	}
	for _, cr := range p.CustomRules {
		if cr.Name == "" {
			return fmt.Errorf("custom rule with empty name")
		}
		if _, err := regexp.Compile("(?i)" + cr.Pattern); err != nil {
			return fmt.Errorf("custom rule %q: invalid pattern: %v", cr.Name, err)
		}
	}
	return nil
}

func (h *Handlers) writePolicyError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handlers) requirePolicyStore(w http.ResponseWriter, r *http.Request) bool {
	if !h.requireAdmin(w, r) {
		return false
	}
	if h.PolicyStore == nil {
		h.writePolicyError(w, "dlp disabled", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// GET /admin/policies → {"default": {...}, "keys": {"<keyID>": {...}}}
func (h *Handlers) handlePoliciesGet(w http.ResponseWriter, r *http.Request) {
	if !h.requirePolicyStore(w, r) {
		return
	}
	def, err := h.PolicyStore.GetDefaultPolicy(r.Context())
	if err != nil {
		h.Logger.Error("get default policy failed", "err", err)
		h.writePolicyError(w, "failed to load policies", http.StatusInternalServerError)
		return
	}
	keys, err := h.PolicyStore.ListPolicies(r.Context())
	if err != nil {
		h.Logger.Error("list policies failed", "err", err)
		h.writePolicyError(w, "failed to load policies", http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = map[string]inspector.Policy{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"default": def, "keys": keys})
}

// PUT /admin/policies/default
func (h *Handlers) handlePolicyPutDefault(w http.ResponseWriter, r *http.Request) {
	if !h.requirePolicyStore(w, r) {
		return
	}
	var p inspector.Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		h.writePolicyError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validatePolicy(p); err != nil {
		h.writePolicyError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.PolicyStore.SetDefaultPolicy(r.Context(), p); err != nil {
		h.Logger.Error("set default policy failed", "err", err)
		h.writePolicyError(w, "failed to save policy", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// PUT /admin/policies/keys/{id}
func (h *Handlers) handlePolicyPutKey(w http.ResponseWriter, r *http.Request) {
	if !h.requirePolicyStore(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" || id == "default" {
		h.writePolicyError(w, "invalid key id", http.StatusBadRequest)
		return
	}
	var p inspector.Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		h.writePolicyError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validatePolicy(p); err != nil {
		h.writePolicyError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.PolicyStore.SetPolicy(r.Context(), id, p); err != nil {
		h.Logger.Error("set key policy failed", "err", err, "key_id", id)
		h.writePolicyError(w, "failed to save policy", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// DELETE /admin/policies/keys/{id} — the key falls back to the default policy.
func (h *Handlers) handlePolicyDeleteKey(w http.ResponseWriter, r *http.Request) {
	if !h.requirePolicyStore(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" || id == "default" {
		h.writePolicyError(w, "invalid key id", http.StatusBadRequest)
		return
	}
	if err := h.PolicyStore.DeletePolicy(r.Context(), id); err != nil {
		h.Logger.Error("delete key policy failed", "err", err, "key_id", id)
		h.writePolicyError(w, "failed to delete policy", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/policies/test — dry-run: what would happen to this text.
func (h *Handlers) handlePolicyTest(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.Inspector == nil {
		h.writePolicyError(w, "dlp disabled", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Text string `json:"text"`
		// KeyID selects whose policy to test; empty means the default policy.
		KeyID string `json:"key_id"`
		// Policy, when set, is tested as-is (unsaved UI state).
		Policy *inspector.Policy `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writePolicyError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	var policy inspector.Policy
	if req.Policy != nil {
		if err := validatePolicy(*req.Policy); err != nil {
			h.writePolicyError(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy = *req.Policy
	} else {
		policy = h.policyFor(r.Context(), req.KeyID)
	}

	findings := h.Inspector.Scan(req.Text, policy)
	verdict := inspector.Verdict(findings)
	if findings == nil {
		findings = []inspector.Finding{}
	}

	resp := map[string]any{
		"verdict":  verdict,
		"findings": findings,
	}
	switch verdict {
	case inspector.ActionBlock:
		resp["upstream_text"] = "" // nothing would be sent
	case inspector.ActionRedact:
		resp["upstream_text"] = inspector.Redact(req.Text, findings)
	default:
		resp["upstream_text"] = req.Text
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
