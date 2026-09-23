package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/oauth"
	"github.com/danilovid/aperture/internal/storage"
)

// Signing in through Google, GitHub and Yandex.
//
// Registration stays closed. A provider account can sign somebody in only if
// it can be tied to a person this installation already knows, or to an
// invitation written for them:
//
//   - an identity connected before signs its person in;
//   - with an invitation in hand, the provider's verified address has to be
//     the invited one, and the account is created (or found) and joined;
//   - otherwise a verified address that matches an existing account connects
//     to it — the only automatic linking there is, and only on an address the
//     provider vouches for;
//   - a signed-in person connecting another way in links it to themselves,
//     whatever its address, having just proved they hold both.
//
// Everything else is refused, which is what "accounts are by invitation"
// means when the person arrives from Google instead of from a link.

const (
	oauthCookie     = "aperture_oauth"
	oauthCookiePath = "/api/auth/oauth/"
)

func (h *Handlers) oauthProvider(id string) *oauth.Provider {
	for _, p := range h.oauthProviders {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// oauthRedirectURI is where the provider sends the browser back. Providers
// accept only the exact address registered with them, so PUBLIC_URL wins over
// anything a request says about itself.
func (h *Handlers) oauthRedirectURI(r *http.Request, p *oauth.Provider) string {
	base := h.publicURL
	if base == "" {
		scheme := "http"
		if secureRequest(r) {
			scheme = "https"
		}
		host := r.Host
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = fwd
		}
		base = scheme + "://" + host
	}
	return base + oauthCookiePath + p.ID + "/callback"
}

// safeNext keeps "where to go afterwards" inside the console. Anything else —
// another site, a protocol-relative "//evil" — is an open redirect waiting to
// happen, so it becomes the console's front page.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/app") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return "/app"
}

// GET /api/auth/providers — which buttons the sign-in page should show.
func (h *Handlers) handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	type provider struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := []provider{}
	if h.AccountStore != nil {
		for _, p := range h.oauthProviders {
			out = append(out, provider{ID: p.ID, Name: p.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// POST /api/auth/oauth/{provider}/start — begin a sign-in. It is a POST that
// answers with the address to go to, rather than a GET that redirects, so
// that starting a flow is something only this page can do: a link on another
// site cannot start one in somebody's browser.
func (h *Handlers) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts are not configured"})
		return
	}
	p := h.oauthProvider(r.PathValue("provider"))
	if p == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "that sign-in method is not configured here"})
		return
	}
	var req struct {
		Next   string `json:"next"`
		Invite string `json:"invite"`
		Link   bool   `json:"link"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
	}
	if req.Invite != "" && !auth.ValidToken(req.Invite) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "this invitation link is not valid"})
		return
	}

	st := oauth.State{
		Provider: p.ID,
		Next:     safeNext(req.Next),
		Invite:   req.Invite,
		Expires:  time.Now().Add(oauth.StateLifetime).Unix(),
	}
	if req.Link {
		c := h.requireUser(w, r)
		if c == nil {
			return
		}
		st.LinkUser = c.User.ID
	}

	var err error
	var challenge string
	if st.Verifier, challenge, err = oauth.NewVerifier(); err == nil {
		st.Nonce, err = oauth.NewNonce()
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not start the sign-in"})
		return
	}
	sealed, err := oauth.Seal(h.oauthStateKey, st)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not start the sign-in"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookie,
		Value:    sealed,
		Path:     oauthCookiePath,
		MaxAge:   int(oauth.StateLifetime.Seconds()),
		HttpOnly: true,
		Secure:   secureRequest(r),
		// Lax is what lets the cookie come back on the provider's redirect,
		// which is a top-level GET from another site.
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"url": p.AuthCodeURL(st.Nonce, challenge, h.oauthRedirectURI(r, p)),
	})
}

// oauthFailure sends the browser back to the page the flow started from, with
// a code the page turns into words. Codes rather than messages, so nothing a
// provider said ends up rendered.
func (h *Handlers) oauthFailure(w http.ResponseWriter, r *http.Request, st *oauth.State, provider, code string) {
	target := "/login"
	if st != nil {
		switch {
		case st.LinkUser != "":
			target = "/app/account"
		case st.Invite != "":
			target = "/invite/" + st.Invite
		}
	}
	q := url.Values{"oauth_error": {code}, "provider": {provider}}
	http.Redirect(w, r, target+"?"+q.Encode(), http.StatusFound)
}

// GET /api/auth/oauth/{provider}/callback — the provider sends the browser
// back here with a code.
func (h *Handlers) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")

	// The state cookie is single-use whatever happens next.
	sealed := ""
	if c, err := r.Cookie(oauthCookie); err == nil {
		sealed = c.Value
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookie, Value: "", Path: oauthCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode,
	})

	p := h.oauthProvider(providerID)
	if p == nil || h.AccountStore == nil {
		h.oauthFailure(w, r, nil, providerID, "not_configured")
		return
	}
	q := r.URL.Query()
	st, err := oauth.Open(h.oauthStateKey, sealed, q.Get("state"), time.Now())
	if err != nil || st.Provider != p.ID {
		h.oauthFailure(w, r, nil, p.ID, "state")
		return
	}
	if q.Get("error") != "" {
		// Usually the person pressed Cancel at the provider.
		h.oauthFailure(w, r, st, p.ID, "denied")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	token, err := p.Exchange(ctx, h.oauthClient, q.Get("code"), st.Verifier, h.oauthRedirectURI(r, p))
	if err != nil {
		h.Logger.Warn("oauth exchange failed", "provider", p.ID, "err", err)
		h.oauthFailure(w, r, st, p.ID, "provider")
		return
	}
	prof, err := p.Profile(ctx, h.oauthClient, token)
	if err != nil {
		h.Logger.Warn("oauth profile failed", "provider", p.ID, "err", err)
		h.oauthFailure(w, r, st, p.ID, "provider")
		return
	}

	if st.LinkUser != "" {
		h.oauthLink(w, r, st, p, prof)
		return
	}

	user, orgID, code := h.oauthResolve(ctx, r, st, p, prof)
	if code != "" {
		h.Logger.Info("oauth sign-in refused", "provider", p.ID, "reason", code)
		h.oauthFailure(w, r, st, p.ID, code)
		return
	}
	_ = h.AccountStore.MarkLogin(ctx, user.ID)
	if orgID == "" {
		if orgs, err := h.AccountStore.OrganizationsOf(ctx, user.ID); err == nil && len(orgs) > 0 {
			orgID = orgs[0].ID
		}
	}
	if _, err := h.issueSession(w, r, user, orgID); err != nil {
		h.Logger.Error("create session failed", "err", err)
		h.oauthFailure(w, r, st, p.ID, "server")
		return
	}
	h.Logger.Info("signed in", "via", p.ID, "user", user.Email)
	http.Redirect(w, r, st.Next, http.StatusFound)
}

// oauthResolve decides whose account a provider sign-in is. It answers with
// the person and, when an invitation brought them, the organization they
// joined; or with a failure code.
func (h *Handlers) oauthResolve(ctx context.Context, r *http.Request, st *oauth.State, p *oauth.Provider, prof *oauth.Profile) (*storage.User, string, string) {
	verified := ""
	if prof.EmailVerified {
		verified = auth.NormalizeEmail(prof.Email)
	}

	user, err := h.AccountStore.UserByIdentity(ctx, p.ID, prof.ID)
	switch {
	case err == nil:
		// Known already. An invitation alongside is theirs to redeem only if
		// it was written for them.
		if user.Disabled {
			return nil, "", "disabled"
		}
		if st.Invite == "" {
			return user, "", ""
		}
		inv, err := h.AccountStore.InvitationByToken(ctx, auth.HashSessionToken(st.Invite))
		if err != nil {
			return nil, "", "invite_invalid"
		}
		invited := auth.NormalizeEmail(inv.Email)
		if invited != auth.NormalizeEmail(user.Email) && invited != verified {
			return nil, "", "invite_email_mismatch"
		}
		orgID, code := h.joinByInvitation(ctx, r, inv, user, p.ID)
		return user, orgID, code

	case !errors.Is(err, storage.ErrUserNotFound):
		h.Logger.Error("identity lookup failed", "err", err)
		return nil, "", "server"
	}

	// A provider account nobody has connected yet.
	if st.Invite != "" {
		inv, err := h.AccountStore.InvitationByToken(ctx, auth.HashSessionToken(st.Invite))
		if err != nil {
			return nil, "", "invite_invalid"
		}
		invited := auth.NormalizeEmail(inv.Email)
		// The invitation names an address; the provider has to vouch that
		// this person owns it. Otherwise a leaked link plus a throwaway
		// provider account would be a way in.
		if verified == "" || verified != invited {
			return nil, "", "invite_email_mismatch"
		}
		user, err := h.AccountStore.UserByEmail(ctx, invited)
		if errors.Is(err, storage.ErrUserNotFound) {
			// No password: this person signs in through the provider.
			user, err = h.AccountStore.CreateUser(ctx, invited, prof.Name, "")
			if errors.Is(err, storage.ErrEmailTaken) {
				user, err = h.AccountStore.UserByEmail(ctx, invited)
			}
		}
		if err != nil {
			h.Logger.Error("account for invitation failed", "err", err)
			return nil, "", "server"
		}
		if user.Disabled {
			return nil, "", "disabled"
		}
		if _, err := h.AccountStore.LinkIdentity(ctx, user.ID, p.ID, prof.ID, prof.Email); err != nil {
			if errors.Is(err, storage.ErrIdentityTaken) {
				return nil, "", "identity_taken"
			}
			return nil, "", "server"
		}
		orgID, code := h.joinByInvitation(ctx, r, inv, user, p.ID)
		return user, orgID, code
	}

	// No invitation: only an existing account, found by an address the
	// provider vouches for.
	if verified == "" {
		return nil, "", "email_unverified"
	}
	user, err = h.AccountStore.UserByEmail(ctx, verified)
	if errors.Is(err, storage.ErrUserNotFound) {
		return nil, "", "no_account"
	}
	if err != nil {
		return nil, "", "server"
	}
	if user.Disabled {
		return nil, "", "disabled"
	}
	if _, err := h.AccountStore.LinkIdentity(ctx, user.ID, p.ID, prof.ID, prof.Email); err != nil {
		if errors.Is(err, storage.ErrIdentityTaken) {
			return nil, "", "identity_taken"
		}
		return nil, "", "server"
	}
	h.Logger.Info("identity connected by verified email", "provider", p.ID, "user", user.Email)
	return user, "", ""
}

// joinByInvitation uses an invitation up and makes its person a member. Being
// a member already is not an error — they clicked the link twice, or were
// added some other way — and the invitation is used up either way.
func (h *Handlers) joinByInvitation(ctx context.Context, r *http.Request, inv *storage.Invitation, user *storage.User, via string) (string, string) {
	_, memberErr := h.AccountStore.MemberRole(ctx, inv.OrgID, user.ID)
	if err := h.AccountStore.AcceptInvitation(ctx, inv.ID); err != nil {
		return "", "invite_invalid"
	}
	if memberErr != nil {
		if err := h.AccountStore.AddMember(ctx, inv.OrgID, user.ID, inv.Role); err != nil {
			h.Logger.Error("add member failed", "err", err, "org", inv.OrgID)
			return "", "server"
		}
		h.auditAs(r, userActor(user), inv.OrgID, "member.join", user.Email,
			map[string]any{"role": inv.Role, "signed_in_with": via})
	}
	return inv.OrgID, ""
}

// oauthLink connects a provider account to the signed-in person.
func (h *Handlers) oauthLink(w http.ResponseWriter, r *http.Request, st *oauth.State, p *oauth.Provider, prof *oauth.Profile) {
	c := callerOf(r)
	// The session has to still be the person who started this. Otherwise a
	// flow started in one account could finish in another.
	if c == nil || c.User.ID != st.LinkUser {
		h.oauthFailure(w, r, nil, p.ID, "session")
		return
	}
	existing, err := h.AccountStore.UserByIdentity(r.Context(), p.ID, prof.ID)
	switch {
	case err == nil && existing.ID != c.User.ID:
		h.oauthFailure(w, r, st, p.ID, "identity_taken")
		return
	case err == nil:
		// Already theirs; nothing to do.
	default:
		if _, err := h.AccountStore.LinkIdentity(r.Context(), c.User.ID, p.ID, prof.ID, prof.Email); err != nil {
			code := "server"
			if errors.Is(err, storage.ErrIdentityTaken) {
				code = "identity_taken"
			}
			h.oauthFailure(w, r, st, p.ID, code)
			return
		}
		h.Logger.Info("identity connected", "provider", p.ID, "user", c.User.Email)
	}
	http.Redirect(w, r, "/app/account?"+url.Values{"connected": {p.ID}}.Encode(), http.StatusFound)
}

// GET /api/auth/identities — the signed-in person's ways in.
func (h *Handlers) handleIdentities(w http.ResponseWriter, r *http.Request) {
	c := h.requireUser(w, r)
	if c == nil {
		return
	}
	list, err := h.AccountStore.IdentitiesOf(r.Context(), c.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load sign-in methods"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"identities":   list,
		"has_password": c.User.PasswordHash != "",
	})
}

// DELETE /api/auth/identities/{id} — disconnect one. Not the last way in:
// an account nobody can sign in to is an account lost.
func (h *Handlers) handleUnlinkIdentity(w http.ResponseWriter, r *http.Request) {
	c := h.requireUser(w, r)
	if c == nil {
		return
	}
	list, err := h.AccountStore.IdentitiesOf(r.Context(), c.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load sign-in methods"})
		return
	}
	if c.User.PasswordHash == "" && len(list) <= 1 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "this is the only way you can sign in; connect another account first",
		})
		return
	}
	if err := h.AccountStore.UnlinkIdentity(r.Context(), c.User.ID, r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such sign-in method"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
