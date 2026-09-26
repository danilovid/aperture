package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mutegate/mutegate/internal/auth"
	"github.com/mutegate/mutegate/internal/storage"
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
	h.audit(r, org.ID, "organization.create", org.Slug, map[string]any{"name": org.Name})
	h.audit(r, org.ID, "member.invite", ownerEmail, map[string]any{"role": storage.RoleOwner})
	writeJSON(w, http.StatusCreated, map[string]any{
		"organization": org,
		"invitation": inviteResponse{
			Invitation: *inv, Token: token, Link: h.inviteLink(r, token),
		},
	})
}

// handleInviteToOrganization lets the operator bring an owner into an
// organization that already exists. It is how anybody first gets into the
// default organization — the one an installation from before multi-tenancy
// keeps all its keys and incidents in, and which was created by a migration
// rather than through this API, so nobody was ever invited to it.
func (h *Handlers) handleInviteToOrganization(w http.ResponseWriter, r *http.Request) {
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
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	email := auth.NormalizeEmail(req.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "email is required and must be an email address"})
		return
	}
	role := storage.Role(strings.TrimSpace(req.Role))
	if role == "" {
		role = storage.RoleOwner
	}
	if !storage.ValidRole(string(role)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown role (want owner, admin, member or viewer)"})
		return
	}

	org, err := h.AccountStore.OrganizationByID(r.Context(), r.PathValue("id"))
	if err != nil || org.Deleted() {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such organization"})
		return
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the invitation"})
		return
	}
	inv, err := h.AccountStore.CreateInvitation(r.Context(), storage.Invitation{
		OrgID: org.ID, Email: email, Role: role, ExpiresAt: time.Now().Add(inviteLifetime),
	}, hash)
	if err != nil {
		h.Logger.Error("create invitation failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the invitation"})
		return
	}
	h.Logger.Info("operator invited into an organization", "org", org.Slug, "email", email, "role", role)
	h.audit(r, org.ID, "member.invite", email, map[string]any{"role": role})
	writeJSON(w, http.StatusCreated, inviteResponse{Invitation: *inv, Token: token, Link: h.inviteLink(r, token)})
}
