package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/mutegate/internal/auth"
	"github.com/danilovid/mutegate/internal/storage"
)

// inviteResponse carries the token exactly once, at creation. There is no way
// to read it back later — the database holds only its hash — so the caller
// either passes the link on now or issues a new invitation.
type inviteResponse struct {
	storage.Invitation
	Token string `json:"token"`
	// Link is the ready-to-send address, built from the request so it matches
	// however this installation is reached.
	Link string `json:"link"`
}

// inviteLink is where the invited person goes to accept. With PUBLIC_URL set
// that is the console's address; without it, the address the request came
// in on — which is the gateway's own when somebody calls it directly, as an
// operator bootstrapping a Compose install does, and the console is not
// served there.
func (h *Handlers) inviteLink(r *http.Request, token string) string {
	if h.publicURL != "" {
		return h.publicURL + "/invite/" + token
	}
	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host + "/invite/" + token
}

// handleCreateInvitation lets an admin bring somebody into the organization.
// With registration closed this is the only door, which is why it is bounded
// by role and by the organization on the session.
func (h *Handlers) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that does not look like an email address"})
		return
	}
	role := storage.Role(strings.TrimSpace(req.Role))
	if role == "" {
		role = storage.RoleMember
	}
	if !storage.ValidRole(string(role)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "unknown role (want owner, admin, member or viewer)",
		})
		return
	}
	// Nobody may invite somebody above themselves: an admin handing out
	// ownership would be a promotion nobody approved.
	if !c.Role.AtLeast(role) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "you cannot invite somebody with more rights than you have",
		})
		return
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the invitation"})
		return
	}
	inv, err := h.AccountStore.CreateInvitation(r.Context(), storage.Invitation{
		OrgID: c.OrgID, Email: email, Role: role, InvitedBy: c.User.ID,
		ExpiresAt: time.Now().Add(inviteLifetime),
	}, hash)
	if err != nil {
		h.Logger.Error("create invitation failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the invitation"})
		return
	}
	h.audit(r, c.OrgID, "member.invite", email, map[string]any{"role": role})
	writeJSON(w, http.StatusCreated, inviteResponse{
		Invitation: *inv, Token: token, Link: h.inviteLink(r, token),
	})
}

// handleLookupInvitation tells the invite page what it is looking at —
// which organization, for which address, in which role — without using the
// invitation up. The page would otherwise ask a stranger for a password
// before saying what the password is for.
//
// The token travels in the body, not the path: paths end up in access logs,
// and an invitation token in a log is an invitation for whoever reads it.
// Unknown, expired and already-used are one answer, as when redeeming.
func (h *Handlers) handleLookupInvitation(w http.ResponseWriter, r *http.Request) {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts are not configured"})
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !auth.ValidToken(req.Token) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	inv, err := h.AccountStore.InvitationByToken(r.Context(), auth.HashSessionToken(req.Token))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	org, err := h.AccountStore.OrganizationByID(r.Context(), inv.OrgID)
	if err != nil || org.Deleted() {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	// Whether an account already exists for the address decides which form
	// the page shows. The token holder was sent this address by whoever
	// invited them, so it reveals nothing they did not already have.
	_, lookupErr := h.AccountStore.UserByEmail(r.Context(), auth.NormalizeEmail(inv.Email))
	writeJSON(w, http.StatusOK, map[string]any{
		"organization":   map[string]string{"name": org.Name, "slug": org.Slug},
		"email":          inv.Email,
		"role":           inv.Role,
		"expires_at":     inv.ExpiresAt,
		"account_exists": lookupErr == nil,
	})
}

func (h *Handlers) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	list, err := h.AccountStore.InvitationsOf(r.Context(), c.OrgID)
	if err != nil {
		h.Logger.Error("list invitations failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load invitations"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": list})
}

func (h *Handlers) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	id := r.PathValue("id")
	var invited *storage.Invitation
	if list, err := h.AccountStore.InvitationsOf(r.Context(), c.OrgID); err == nil {
		for i := range list {
			if list[i].ID == id {
				invited = &list[i]
			}
		}
	}
	if err := h.AccountStore.RevokeInvitation(r.Context(), c.OrgID, id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such pending invitation"})
		return
	}
	if invited != nil {
		h.audit(r, c.OrgID, "member.uninvite", invited.Email, map[string]any{"role": invited.Role})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMembers lists who is in the organization. Any member may see it —
// knowing who your colleagues are is not privileged information, and hiding
// it only makes the roles screen useless to the people who use it.
func (h *Handlers) handleMembers(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleViewer)
	if c == nil {
		return
	}
	members, err := h.AccountStore.MembersOf(r.Context(), c.OrgID)
	if err != nil {
		h.Logger.Error("list members failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load members"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// handleSetMemberRole changes somebody's role. Two rules keep an organization
// from locking itself out or from being taken over: nobody may grant rights
// above their own, and the last owner cannot be demoted.
func (h *Handlers) handleSetMemberRole(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !storage.ValidRole(req.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "unknown role (want owner, admin, member or viewer)",
		})
		return
	}
	role := storage.Role(req.Role)
	if !c.Role.AtLeast(role) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "you cannot grant more rights than you have",
		})
		return
	}
	userID := r.PathValue("id")
	if err := h.guardLastOwner(r, c.OrgID, userID, role); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	member := h.memberOf(r, c.OrgID, userID)
	if err := h.AccountStore.SetMemberRole(r.Context(), c.OrgID, userID, role); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such member"})
		return
	}
	if member != nil && member.Role != role {
		h.audit(r, c.OrgID, "member.role", member.Email, map[string]any{
			"changes": []string{"role: " + string(member.Role) + " → " + string(role)},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	userID := r.PathValue("id")
	if err := h.guardLastOwner(r, c.OrgID, userID, ""); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	member := h.memberOf(r, c.OrgID, userID)
	if err := h.AccountStore.RemoveMember(r.Context(), c.OrgID, userID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such member"})
		return
	}
	if member != nil {
		h.audit(r, c.OrgID, "member.remove", member.Email, map[string]any{"role": member.Role})
	}
	// Their sessions still point at this organization; dropping them means
	// the next request re-resolves and finds no membership.
	_ = h.AccountStore.RevokeUserSessions(r.Context(), userID)
	w.WriteHeader(http.StatusNoContent)
}

// memberOf finds one member, to name them in the audit log.
func (h *Handlers) memberOf(r *http.Request, orgID, userID string) *storage.Member {
	members, err := h.AccountStore.MembersOf(r.Context(), orgID)
	if err != nil {
		return nil
	}
	for i := range members {
		if members[i].ID == userID {
			return &members[i]
		}
	}
	return nil
}

// guardLastOwner refuses a change that would leave the organization with no
// owner. newRole empty means the member is being removed.
func (h *Handlers) guardLastOwner(r *http.Request, orgID, userID string, newRole storage.Role) error {
	current, err := h.AccountStore.MemberRole(r.Context(), orgID, userID)
	if err != nil || current != storage.RoleOwner || newRole == storage.RoleOwner {
		return nil
	}
	members, err := h.AccountStore.MembersOf(r.Context(), orgID)
	if err != nil {
		return nil
	}
	owners := 0
	for _, m := range members {
		if m.Role == storage.RoleOwner {
			owners++
		}
	}
	if owners <= 1 {
		return errLastOwner
	}
	return nil
}

var errLastOwner = &lastOwnerError{}

type lastOwnerError struct{}

func (*lastOwnerError) Error() string {
	return "this is the last owner; promote somebody else first"
}
