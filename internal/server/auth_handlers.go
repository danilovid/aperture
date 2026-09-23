package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/danilovid/mutegate/internal/auth"
	"github.com/danilovid/mutegate/internal/storage"
)

// Sign-in attempt limiting. The numbers are deliberately gentle: the point is
// to make guessing passwords expensive, not to lock people out of their own
// account because they mistyped it four times.
const (
	maxLoginAttempts = 8
	loginWindow      = 15 * time.Minute
	inviteLifetime   = 7 * 24 * time.Hour
	// Sign-ups are counted per address, successful or not: an open form that
	// answers "that address is taken" is also a way to find out who has an
	// account, and a way to fill the database with organizations.
	maxSignups   = 10
	signupWindow = time.Hour
)

// loginLimiter counts recent attempts per key. It lives in memory on purpose:
// behind several instances each one limits its own share, which is the same
// trade-off the request rate limiter already makes.
type loginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter {
	return newLimiter(maxLoginAttempts, loginWindow)
}

func newLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, attempts: map[string][]time.Time{}}
}

func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) >= l.max
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[key] = append(l.recent(key), time.Now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// recent drops the attempts that have aged out. Called with the lock held.
func (l *loginLimiter) recent(key string) []time.Time {
	cutoff := time.Now().Add(-l.window)
	kept := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.attempts[key] = kept
	return kept
}

// meResponse is what the console asks for on every load: who am I, where am I,
// and what else could I switch to.
type meResponse struct {
	User          *storage.User           `json:"user"`
	Organization  *storage.Organization   `json:"organization,omitempty"`
	Role          storage.Role            `json:"role,omitempty"`
	Organizations []storage.OrgMembership `json:"organizations"`
}

func (h *Handlers) meFor(r *http.Request, c *caller) (*meResponse, error) {
	orgs, err := h.AccountStore.OrganizationsOf(r.Context(), c.User.ID)
	if err != nil {
		return nil, err
	}
	out := &meResponse{User: c.User, Role: c.Role, Organizations: orgs}
	if c.OrgID != "" {
		org, err := h.AccountStore.OrganizationByID(r.Context(), c.OrgID)
		if err == nil {
			out.Organization = org
		}
	}
	return out, nil
}

// handleRegister creates an account from an invitation. Registration is closed
// — there is no other way in — so the invitation is what proves someone is
// meant to be here, and it also decides which organization and role they get.
func (h *Handlers) handleRegister(w http.ResponseWriter, r *http.Request) {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts are not configured"})
		return
	}
	var req struct {
		Token    string `json:"token"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
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
	if !auth.ValidToken(req.Token) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "this invitation link is not valid"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	inv, err := h.AccountStore.InvitationByToken(r.Context(), auth.HashSessionToken(req.Token))
	if err != nil {
		// Unknown, expired and already used are one answer on purpose:
		// probing tokens should teach nothing.
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	// The invitation names the person it was written for. Letting anybody
	// redeem it would turn one leaked link into an open door.
	if auth.NormalizeEmail(inv.Email) != email {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this invitation was issued for a different email address",
		})
		return
	}

	user, err := h.AccountStore.CreateUser(r.Context(), email, req.Name, hash)
	if err != nil {
		if errors.Is(err, storage.ErrEmailTaken) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "an account with this address already exists — sign in instead",
			})
			return
		}
		h.Logger.Error("create user failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the account"})
		return
	}

	// Accepting before adding the member makes the link single-use even if
	// two registrations race: the loser never becomes a member.
	if err := h.AccountStore.AcceptInvitation(r.Context(), inv.ID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "this invitation is no longer valid"})
		return
	}
	if err := h.AccountStore.AddMember(r.Context(), inv.OrgID, user.ID, inv.Role); err != nil {
		h.Logger.Error("add member failed", "err", err, "org", inv.OrgID)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not join the organization"})
		return
	}
	h.auditAs(r, userActor(user), inv.OrgID, "member.join", user.Email, map[string]any{"role": inv.Role})

	// Registering signs the person in, so it counts as a sign-in: otherwise the
	// members screen says "never" about somebody who is looking at it.
	_ = h.AccountStore.MarkLogin(r.Context(), user.ID)
	h.startSession(w, r, user, inv.OrgID)
}

// handleLogin signs a person in. Every failure answers the same way, at the
// same cost: otherwise the form tells a stranger which addresses exist here.
func (h *Handlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts are not configured"})
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	email := auth.NormalizeEmail(req.Email)
	limitKey := clientIP(r) + "|" + email

	if h.logins.blocked(limitKey) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "too many attempts; try again in a few minutes",
		})
		return
	}

	const refusal = "wrong email or password"
	user, err := h.AccountStore.UserByEmail(r.Context(), email)
	if err != nil || user.PasswordHash == "" || user.Disabled {
		// Spend the work a real verification would, so a missing account is
		// not measurably faster than a wrong password.
		auth.SpendVerifyTime()
		h.logins.fail(limitKey)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": refusal})
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil || !ok {
		h.logins.fail(limitKey)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": refusal})
		return
	}
	h.logins.reset(limitKey)

	// Parameters get stronger over time; upgrade the stored hash while we
	// have the plaintext in hand and it is already verified.
	if auth.NeedsRehash(user.PasswordHash) {
		if newHash, err := auth.HashPassword(req.Password); err == nil {
			_ = h.AccountStore.SetPasswordHash(r.Context(), user.ID, newHash)
		}
	}
	_ = h.AccountStore.MarkLogin(r.Context(), user.ID)

	orgID := ""
	if orgs, err := h.AccountStore.OrganizationsOf(r.Context(), user.ID); err == nil && len(orgs) > 0 {
		orgID = orgs[0].ID
	}
	h.startSession(w, r, user, orgID)
}

// issueSession creates a session and sets its cookie. It is the half of
// signing in that every way of signing in shares; what to answer afterwards
// is the caller's business.
func (h *Handlers) issueSession(w http.ResponseWriter, r *http.Request, user *storage.User, orgID string) (*storage.Session, error) {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(auth.SessionLifetime)
	sess, err := h.AccountStore.CreateSession(r.Context(), storage.Session{
		UserID: user.ID, OrgID: orgID, ExpiresAt: expires,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	}, hash)
	if err != nil {
		return nil, err
	}
	h.setSessionCookie(w, r, token, expires)
	return sess, nil
}

// startSession issues a session and answers with the same shape as /me, so the
// console can render straight from a sign-in or a registration.
func (h *Handlers) startSession(w http.ResponseWriter, r *http.Request, user *storage.User, orgID string) {
	sess, err := h.issueSession(w, r, user, orgID)
	if err != nil {
		h.Logger.Error("create session failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not start a session"})
		return
	}
	c := &caller{User: user, Session: sess, OrgID: orgID}
	if orgID != "" {
		if role, err := h.AccountStore.MemberRole(r.Context(), orgID, user.ID); err == nil {
			c.Role = role
		}
	}
	me, err := h.meFor(r, c)
	if err != nil {
		h.Logger.Error("me failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the account"})
		return
	}
	writeJSON(w, http.StatusOK, me)
}

func (h *Handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	c := callerOf(r)
	if c != nil && h.AccountStore != nil {
		everywhere := r.URL.Query().Get("everywhere") == "true"
		if everywhere {
			_ = h.AccountStore.RevokeUserSessions(r.Context(), c.User.ID)
		} else {
			_ = h.AccountStore.RevokeSession(r.Context(), c.Session.ID)
		}
	}
	// The cookie goes regardless: a caller with a dead session must not be
	// left holding one that looks alive.
	h.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) handleMe(w http.ResponseWriter, r *http.Request) {
	c := h.requireUser(w, r)
	if c == nil {
		return
	}
	me, err := h.meFor(r, c)
	if err != nil {
		h.Logger.Error("me failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the account"})
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// handleSwitchOrg moves the session into another organization. The store
// refuses one the person does not belong to, so this cannot be talked into
// showing a stranger's data by passing an id.
func (h *Handlers) handleSwitchOrg(w http.ResponseWriter, r *http.Request) {
	c := h.requireUser(w, r)
	if c == nil {
		return
	}
	var req struct {
		OrgID string `json:"org_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "org_id is required"})
		return
	}
	if err := h.AccountStore.SetSessionOrg(r.Context(), c.Session.ID, req.OrgID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "you are not a member of that organization"})
		return
	}
	c.OrgID = req.OrgID
	if role, err := h.AccountStore.MemberRole(r.Context(), req.OrgID, c.User.ID); err == nil {
		c.Role = role
	}
	me, err := h.meFor(r, c)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the account"})
		return
	}
	writeJSON(w, http.StatusOK, me)
}
