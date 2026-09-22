package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/storage"
)

// The instance admin is the operator of the installation, authenticated with
// ADMIN_API_KEY. It exists to bring the first organization into being and
// nothing else: it cannot read anybody's incidents, keys or policies. Those
// belong to organizations, and the operator is not a member of any.

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// slugify turns a name into a usable slug so the caller does not have to.
func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// handleCreateOrganization creates an organization and the invitation its
// first owner will register with. Everybody arrives through an invitation,
// including the first person — one way in is one thing to get right.
func (h *Handlers) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "accounts are not configured (this gateway runs without a database)",
		})
		return
	}
	var req struct {
		Name       string `json:"name"`
		Slug       string `json:"slug"`
		OwnerEmail string `json:"owner_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	ownerEmail := auth.NormalizeEmail(req.OwnerEmail)
	if !auth.ValidEmail(ownerEmail) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "owner_email is required and must be an email address",
		})
		return
	}
	slug := strings.TrimSpace(strings.ToLower(req.Slug))
	if slug == "" {
		slug = slugify(name)
	}
	if !slugPattern.MatchString(slug) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "slug must be lowercase letters, digits and dashes, 1–40 characters",
		})
		return
	}

	org, err := h.AccountStore.CreateOrganization(r.Context(), name, slug)
	if err != nil {
		if errors.Is(err, storage.ErrSlugTaken) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "that slug is taken"})
			return
		}
		h.Logger.Error("create organization failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the organization"})
		return
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the invitation"})
		return
	}
	inv, err := h.AccountStore.CreateInvitation(r.Context(), storage.Invitation{
		OrgID: org.ID, Email: ownerEmail, Role: storage.RoleOwner,
		ExpiresAt: time.Now().Add(inviteLifetime),
	}, hash)
	if err != nil {
		h.Logger.Error("create owner invitation failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the invitation"})
		return
	}

	h.Logger.Info("organization created", "org", org.Slug, "owner", ownerEmail)
	writeJSON(w, http.StatusCreated, map[string]any{
		"organization": org,
		"invitation": inviteResponse{
			Invitation: *inv, Token: token, Link: inviteLink(r, token),
		},
	})
}
