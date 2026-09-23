package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/storage"
)

// csrfCookie carries the value the browser must echo back in the CSRF header.
// Unlike the session cookie it is readable by JavaScript on purpose: the SPA
// has to read it to send it. Knowing it is useless without also being able to
// send the session cookie, which a cross-site page cannot do under SameSite.
const csrfCookie = "aperture_csrf"

// caller is the person behind a request, resolved once by the session
// middleware and read from the context by everything downstream.
type caller struct {
	User    *storage.User
	Session *storage.Session
	// OrgID is the organization this request acts in. Empty means the person
	// is signed in but belongs to no organization yet.
	OrgID string
	Role  storage.Role
}

type callerKey struct{}

// withCaller returns a request carrying the signed-in person.
func withCaller(r *http.Request, c *caller) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), callerKey{}, c))
}

// callerOf returns the signed-in person, or nil when the request is anonymous.
func callerOf(r *http.Request) *caller {
	c, _ := r.Context().Value(callerKey{}).(*caller)
	return c
}

// sessionMiddleware resolves the session cookie into a caller. It never
// rejects anything: a request without a session simply carries no caller, and
// the handlers that need one say so. That keeps the landing page, the health
// checks and the agent APIs out of the session machinery entirely.
func (h *Handlers) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.AccountStore == nil {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(auth.SessionCookie)
		if err != nil || !auth.ValidToken(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}

		sess, user, err := h.AccountStore.SessionByToken(r.Context(), auth.HashSessionToken(cookie.Value))
		if err != nil {
			// Expired, revoked or forged: clear the cookie so the browser
			// stops presenting it on every request from here on.
			if errors.Is(err, storage.ErrSessionNotFound) {
				h.clearSessionCookie(w, r)
			}
			next.ServeHTTP(w, r)
			return
		}
		if user.Disabled {
			h.clearSessionCookie(w, r)
			next.ServeHTTP(w, r)
			return
		}

		c := &caller{User: user, Session: sess, OrgID: sess.OrgID}
		if c.OrgID != "" {
			// Membership can be revoked while a session is open, so the role
			// is read per request rather than trusted from the session.
			role, err := h.AccountStore.MemberRole(r.Context(), c.OrgID, user.ID)
			if err != nil {
				c.OrgID, c.Role = "", ""
			} else {
				c.Role = role
			}
		}
		if c.OrgID != "" {
			// An organization can be closed while somebody is looking at it.
			// Dropping it here means every handler below sees "no
			// organization" rather than each having to remember to ask.
			if org, err := h.AccountStore.OrganizationByID(r.Context(), c.OrgID); err != nil || org.Deleted() {
				c.OrgID, c.Role = "", ""
			}
		}

		// Extend a session that is being used, but not on every request: one
		// database write per page view buys nothing.
		if time.Until(sess.ExpiresAt) < auth.SessionLifetime-auth.SessionRenewAfter {
			_ = h.AccountStore.RenewSession(r.Context(), sess.ID, time.Now().Add(auth.SessionLifetime))
		}
		next.ServeHTTP(w, withCaller(r, c))
	})
}

// csrfMiddleware refuses cross-site writes. A browser will happily send the
// session cookie on a form post from another origin; it cannot set a custom
// header there, and it cannot read our CSRF cookie to echo it back. The
// combination is what makes the request provably same-origin.
func (h *Handlers) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// Only cookie-authenticated requests are at risk. Agents and CI
		// present a bearer token, which no other site can make a browser send.
		if _, err := r.Cookie(auth.SessionCookie); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		// The agent APIs never read the session: they authenticate with an
		// aperture key and nothing else. A browser that is signed in still
		// sends its cookie along — the console's playground does — and
		// asking it for a token that proves nothing there only breaks it.
		if agentAPI(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		sent := r.Header.Get(auth.CSRFHeader)
		cookie, err := r.Cookie(csrfCookie)
		if err != nil || sent == "" ||
			subtle.ConstantTimeCompare([]byte(sent), []byte(cookie.Value)) != 1 {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "missing or invalid CSRF token; send the " + auth.CSRFHeader +
					" header with the value of the " + csrfCookie + " cookie",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// agentAPI reports whether a path is one of the APIs agents call with an
// aperture key: the OpenAI and Anthropic shapes under /v1, and Jev's.
func agentAPI(path string) bool {
	return strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/api/v1/")
}

// requireUser returns the caller, or writes 401 and returns nil.
func (h *Handlers) requireUser(w http.ResponseWriter, r *http.Request) *caller {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "accounts are not configured (this gateway runs without a database)",
		})
		return nil
	}
	c := callerOf(r)
	if c == nil {
		// A service token reaching here is not "not signed in", it is in the
		// wrong place: these endpoints are about people — who is in the
		// organization, who may invite whom — and a machine has no business
		// with them. Saying so saves somebody an hour with the wrong theory.
		if strings.HasPrefix(extractBearerToken(r), serviceTokenPrefix) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "this endpoint is not available to service tokens; sign in to use it",
			})
			return nil
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "not signed in"})
		return nil
	}
	return c
}

// requireRole returns the caller only when they act inside an organization
// with at least the given role. Membership is checked first and separately:
// a role never grants access to an organization somebody does not belong to.
func (h *Handlers) requireRole(w http.ResponseWriter, r *http.Request, min storage.Role) *caller {
	c := h.requireUser(w, r)
	if c == nil {
		return nil
	}
	if c.OrgID == "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "no organization selected"})
		return nil
	}
	if !c.Role.AtLeast(min) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this action requires the " + string(min) + " role",
		})
		return nil
	}
	return c
}

// ── cookies ──────────────────────────────────────────────────────────────────

// secureRequest reports whether the browser reached us over TLS, directly or
// through a proxy that says so. Marking a cookie Secure over plain http means
// the browser drops it, which would make local development impossible.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (h *Handlers) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true, // JavaScript must never be able to read it
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	// A fresh CSRF value per sign-in, readable by the SPA.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err == nil {
		http.SetCookie(w, &http.Cookie{
			Name:     csrfCookie,
			Value:    base64.RawURLEncoding.EncodeToString(raw),
			Path:     "/",
			Expires:  expires,
			HttpOnly: false,
			Secure:   secureRequest(r),
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func (h *Handlers) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{auth.SessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: name == auth.SessionCookie,
			Secure:   secureRequest(r),
			SameSite: http.SameSiteLaxMode,
		})
	}
}

// clientIP is the address attempt limiting counts against. Behind a proxy the
// socket address is the proxy, so the forwarded header is used when present —
// it is only as trustworthy as the proxy in front, which is why the fallback
// is the socket.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, ok := strings.Cut(fwd, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

// adminOrg resolves which organization an administrative request acts in, and
// whether the caller may act there at all.
//
// Three kinds of caller reach these endpoints. A person in the console carries
// a session, and the organization comes from it. CI carries a service token,
// which belongs to one organization and says what it may do. The operator of
// the installation carries ADMIN_API_KEY, which owns no organization: it must
// name one with X-Aperture-Org, and on a single-tenant install that is the
// default organization. Either way the answer is one organization id, and
// every store call below it is scoped to that id.
func (h *Handlers) adminOrg(w http.ResponseWriter, r *http.Request, min storage.Role) (string, bool) {
	if c := callerOf(r); c != nil {
		if c.OrgID == "" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "no organization selected"})
			return "", false
		}
		if !c.Role.AtLeast(min) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "this action requires the " + string(min) + " role",
			})
			return "", false
		}
		return c.OrgID, true
	}

	if strings.HasPrefix(extractBearerToken(r), serviceTokenPrefix) {
		return h.serviceTokenOrg(w, r, min)
	}

	// No session and no service token: fall back to the operator's key.
	if !h.requireAdmin(w, r) {
		return "", false
	}
	orgID := strings.TrimSpace(r.Header.Get("X-Aperture-Org"))
	if orgID == "" {
		orgID = storage.DefaultOrgID
	}
	// Worth a line in the log: this is the one path where a credential that
	// belongs to no organization reads one organization's data.
	h.Logger.Info("instance admin acting on an organization",
		"org", orgID, "path", r.URL.Path, "ip", clientIP(r))
	return orgID, true
}

// serviceTokenOrg resolves a machine caller. Two things have to be true, and
// they are different things: the token's scopes must add up to the role the
// endpoint asks for, and the token must carry the scope this particular
// endpoint needs. The first stops a read-only token from writing; the second
// stops a token issued for the incident feed from touching keys.
func (h *Handlers) serviceTokenOrg(w http.ResponseWriter, r *http.Request, min storage.Role) (string, bool) {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "service tokens need a database (this gateway runs without one)",
		})
		return "", false
	}
	presented := extractBearerToken(r)
	token, err := h.AccountStore.ServiceTokenByHash(r.Context(), auth.HashSessionToken(presented))
	if err != nil {
		// Unknown, revoked, expired and belonging-to-a-deleted-organization
		// are one answer, as everywhere else a credential is presented.
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return "", false
	}

	scope, reachable := scopeForRequest(r)
	if !reachable {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this endpoint is not available to service tokens; sign in to use it",
		})
		return "", false
	}
	if !token.Allows(scope) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this token does not carry the " + string(scope) + " scope",
		})
		return "", false
	}
	if !token.Role().AtLeast(min) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this action requires the " + string(min) + " role",
		})
		return "", false
	}

	// Best effort, and deliberately not fatal: knowing a token is in use is
	// worth having, and failing a request because that note did not land
	// would be absurd.
	if err := h.AccountStore.TouchServiceToken(r.Context(), token.OrgID, token.ID); err != nil {
		h.Logger.Debug("could not record service token use", "err", err)
	}
	return token.OrgID, true
}
