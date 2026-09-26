package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mutegate/mutegate/internal/auth"
	"github.com/mutegate/mutegate/internal/storage"
)

// What an organization's own people can do to it: rename it, leave it, close
// it. Creating one is the operator's job (see instance.go) and restoring a
// closed one is too — once an organization is deleted nobody inside it can
// sign in to undo that, which is the point of a recovery window rather than
// an undo button.

// handleRenameOrganization changes the display name. The slug stays: it is
// how the organization is referred to from outside, and renaming things that
// other things point at is how links rot.
func (h *Handlers) handleRenameOrganization(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "name is required and must be at most 200 characters",
		})
		return
	}
	before, err := h.AccountStore.OrganizationByID(r.Context(), c.OrgID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such organization"})
		return
	}
	org, err := h.AccountStore.RenameOrganization(r.Context(), c.OrgID, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such organization"})
		return
	}
	if before.Name != org.Name {
		h.audit(r, c.OrgID, "organization.rename", org.Slug, map[string]any{
			"changes": []string{"name: " + before.Name + " → " + org.Name},
		})
	}
	h.Logger.Info("organization renamed", "org", org.Slug, "by", c.User.Email)
	writeJSON(w, http.StatusOK, map[string]any{"organization": org})
}

// handleDeleteOrganization closes an organization. It is soft: the rows stay
// where they are so a mistake is recoverable, and everything stops working —
// the console, the agents' keys, the service tokens. A deletion that left the
// gateway serving traffic would not be a deletion.
func (h *Handlers) handleDeleteOrganization(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleOwner)
	if c == nil {
		return
	}
	// Closing an organization is not something to do by accident, so the
	// caller has to name the one they mean.
	var req struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	org, err := h.AccountStore.OrganizationByID(r.Context(), c.OrgID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such organization"})
		return
	}
	if strings.TrimSpace(req.Slug) != org.Slug {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": `to delete this organization, send its slug: {"slug":"` + org.Slug + `"}`,
		})
		return
	}
	if err := h.AccountStore.DeleteOrganization(r.Context(), c.OrgID); err != nil {
		h.Logger.Error("delete organization failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not delete the organization"})
		return
	}
	// Nobody can read it until an operator restores the organization, and
	// then it is the first thing they will want to see.
	h.audit(r, c.OrgID, "organization.delete", org.Slug, nil)
	h.Logger.Warn("organization deleted", "org", org.Slug, "by", c.User.Email,
		"note", "soft delete — the data is still there and an operator can restore it")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"note": "the organization is closed and its keys have stopped working; " +
			"the data is kept, so an operator can restore it",
	})
}

// handleRestoreOrganization is the operator's undo. It is here and not in the
// console because a deleted organization has no working console.
func (h *Handlers) handleRestoreOrganization(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "accounts are not configured (this gateway runs without a database)",
		})
		return
	}
	orgID := r.PathValue("id")
	if err := h.AccountStore.RestoreOrganization(r.Context(), orgID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such organization"})
		return
	}
	h.audit(r, orgID, "organization.restore", "", nil)
	org, err := h.AccountStore.OrganizationByID(r.Context(), orgID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	h.Logger.Info("organization restored", "org", org.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"organization": org})
}

// handleLeaveOrganization removes the caller from the organization they are
// in. The last owner may not leave, for the same reason they may not be
// demoted: an organization with no owner is one nobody can administer.
func (h *Handlers) handleLeaveOrganization(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleViewer)
	if c == nil {
		return
	}
	if err := h.guardLastOwner(r, c.OrgID, c.User.ID, ""); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err := h.AccountStore.RemoveMember(r.Context(), c.OrgID, c.User.ID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "you are not a member of this organization"})
		return
	}
	h.audit(r, c.OrgID, "member.leave", c.User.Email, map[string]any{"role": c.Role})

	// The session still points at the organization they just left. Move it to
	// another one they belong to, or to none — either way the next request
	// must not find them still inside.
	next := ""
	if orgs, err := h.AccountStore.OrganizationsOf(r.Context(), c.User.ID); err == nil && len(orgs) > 0 {
		next = orgs[0].ID
	}
	if next != "" {
		_ = h.AccountStore.SetSessionOrg(r.Context(), c.Session.ID, next)
		c.OrgID = next
		if role, err := h.AccountStore.MemberRole(r.Context(), next, c.User.ID); err == nil {
			c.Role = role
		}
	} else {
		_ = h.AccountStore.RevokeUserSessions(r.Context(), c.User.ID)
		h.clearSessionCookie(w, r)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":   true,
			"note": "that was your only organization, so you have been signed out",
		})
		return
	}
	me, err := h.meFor(r, c)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the account"})
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// handleAcceptInvitation joins the signed-in person to another organization.
// Registration redeems an invitation for somebody who has no account yet;
// this is the same door for somebody who already does, and without it an
// existing user invited elsewhere would be told their address is taken and
// left standing outside.
func (h *Handlers) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	c := h.requireUser(w, r)
	if c == nil {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !auth.ValidToken(req.Token) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "this invitation link is not valid"})
		return
	}
	inv, err := h.AccountStore.InvitationByToken(r.Context(), auth.HashSessionToken(req.Token))
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	// The invitation names the person it was written for. Somebody signed in
	// as a different account must not be able to consume it — including by
	// accident, which is the likelier way it would happen.
	if auth.NormalizeEmail(inv.Email) != auth.NormalizeEmail(c.User.Email) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this invitation was issued for a different email address",
		})
		return
	}
	if _, err := h.AccountStore.MemberRole(r.Context(), inv.OrgID, c.User.ID); err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "you are already a member of that organization",
		})
		return
	} else if !errors.Is(err, storage.ErrNotMember) {
		h.Logger.Error("membership check failed", "err", err)
	}

	// Accept first: that is what makes the link single-use even if two
	// requests race, and the loser never becomes a member.
	if err := h.AccountStore.AcceptInvitation(r.Context(), inv.ID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	if err := h.AccountStore.AddMember(r.Context(), inv.OrgID, c.User.ID, inv.Role); err != nil {
		h.Logger.Error("add member failed", "err", err, "org", inv.OrgID)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not join the organization"})
		return
	}
	// Filed in the organization joined, not the one the session was in.
	h.auditAs(r, userActor(c.User), inv.OrgID, "member.join", c.User.Email, map[string]any{"role": inv.Role})

	// Land them in the organization they just joined: that is what they were
	// invited to look at.
	if err := h.AccountStore.SetSessionOrg(r.Context(), c.Session.ID, inv.OrgID); err == nil {
		c.OrgID, c.Role = inv.OrgID, inv.Role
	}
	h.Logger.Info("invitation accepted", "org", inv.OrgID, "user", c.User.Email, "role", inv.Role)
	me, err := h.meFor(r, c)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the account"})
		return
	}
	writeJSON(w, http.StatusOK, me)
}
