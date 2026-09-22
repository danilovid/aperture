package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/oauth"
	"github.com/danilovid/aperture/internal/storage"
)

// fakeIdP stands in for Google. It remembers the PKCE challenge and redirect
// address a sign-in started with, and refuses to hand out a token unless the
// exchange presents the matching verifier and the same address — which is
// what a real provider does, and what the gateway has to get right.
type fakeIdP struct {
	mu        sync.Mutex
	challenge string
	redirect  string
	profile   map[string]any
	srv       *httptest.Server
}

func newFakeIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge ||
			r.Form.Get("redirect_uri") != f.redirect ||
			r.Form.Get("client_secret") != "google-secret" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.profile)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) as(sub, email string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profile = map[string]any{"sub": sub, "email": email, "email_verified": verified, "name": "From Google"}
}

type oauthEnv struct {
	t        *testing.T
	h        http.Handler
	accounts *storage.MemAccountStore
	idp      *fakeIdP
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	idp := newFakeIdP(t)
	google := oauth.Google("google-client", "google-secret")
	google.TokenURL = idp.srv.URL + "/token"
	google.UserInfoURL = idp.srv.URL + "/userinfo"

	accounts := storage.NewMemAccountStore()
	h := Routes(Options{
		KeyStore:       config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore:   accounts,
		AdminAPIKey:    "instance-admin",
		OAuthProviders: []*oauth.Provider{google},
		OAuthStateKey:  oauth.DeriveKey("test"),
		PublicURL:      "https://gw.test",
		Logger:         slog.Default(),
	})
	return &oauthEnv{t: t, h: h, accounts: accounts, idp: idp}
}

// signInWithGoogle runs the whole flow in one browser: start, "go to Google",
// come back with a code. It returns where the gateway sent the browser.
func (e *oauthEnv) signInWithGoogle(c *client, body string) *url.URL {
	e.t.Helper()
	rec := c.do(http.MethodPost, "/api/auth/oauth/google/start", body)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct{ URL string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	auth, _ := url.Parse(out.URL)
	q := auth.Query()

	e.idp.mu.Lock()
	e.idp.challenge = q.Get("code_challenge")
	e.idp.redirect = q.Get("redirect_uri")
	e.idp.mu.Unlock()

	back := c.do(http.MethodGet, "/api/auth/oauth/google/callback?"+url.Values{
		"code": {"good-code"}, "state": {q.Get("state")},
	}.Encode(), "")
	if back.Code != http.StatusFound {
		e.t.Fatalf("callback = %d: %s", back.Code, back.Body.String())
	}
	loc, _ := url.Parse(back.Header().Get("Location"))
	return loc
}

func (e *oauthEnv) invite(orgName, email string) string {
	e.t.Helper()
	return bootstrap(e.t, e.h, orgName, email)
}

func (e *oauthEnv) signedIn(c *client) *meResponse {
	e.t.Helper()
	rec := c.do(http.MethodGet, "/api/auth/me", "")
	if rec.Code != http.StatusOK {
		return nil
	}
	me := c.me(rec)
	return &me
}

func TestOAuthStartSendsPKCEAndPinsTheRedirect(t *testing.T) {
	e := newOAuthEnv(t)
	c := newClient(t, e.h)
	rec := c.do(http.MethodPost, "/api/auth/oauth/google/start", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct{ URL string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	u, _ := url.Parse(out.URL)
	q := u.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" {
		t.Errorf("the provider URL lacks PKCE or state: %s", out.URL)
	}
	// PUBLIC_URL, not the request's Host, decides where the provider returns.
	if q.Get("redirect_uri") != "https://gw.test/api/auth/oauth/google/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if _, ok := c.cookies[oauthCookie]; !ok {
		t.Error("no state cookie was set")
	}
	if strings.Contains(rec.Body.String(), "google-secret") {
		t.Error("the client secret reached the browser")
	}
}

// With an invitation in hand, the provider's verified address has to be the
// invited one; then the account is created, without a password, and joined.
func TestOAuthRedeemsAnInvitationForTheRightAddress(t *testing.T) {
	e := newOAuthEnv(t)
	token := e.invite("Acme", "ann@acme.test")
	e.idp.as("g-ann", "ann@acme.test", true)

	c := newClient(t, e.h)
	loc := e.signInWithGoogle(c, `{"invite":"`+token+`"}`)
	if loc.Path != "/app" {
		t.Fatalf("sent to %s, want the console", loc)
	}
	me := e.signedIn(c)
	if me == nil || me.Organization == nil || me.Organization.Name != "Acme" || me.Role != storage.RoleOwner {
		t.Fatalf("not signed in to Acme as owner: %+v", me)
	}
	u, _ := e.accounts.UserByEmail(context.Background(), "ann@acme.test")
	if u.PasswordHash != "" {
		t.Error("an account made through Google was given a password")
	}
	// And Google signs her in from now on, without the invitation.
	c2 := newClient(t, e.h)
	if loc := e.signInWithGoogle(c2, `{}`); loc.Path != "/app" || e.signedIn(c2) == nil {
		t.Errorf("a connected identity did not sign in again: %s", loc)
	}
	// A password sign-in finds nothing to check against, and says what it
	// always says.
	rec := newClient(t, e.h).do(http.MethodPost, "/api/auth/login", `{"email":"ann@acme.test","password":"anything at all"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("password sign-in to a Google-only account = %d", rec.Code)
	}
}

func TestOAuthRefusesAnInvitationForAnotherAddress(t *testing.T) {
	e := newOAuthEnv(t)
	token := e.invite("Acme", "ann@acme.test")
	e.idp.as("g-mallory", "mallory@example.test", true)

	c := newClient(t, e.h)
	loc := e.signInWithGoogle(c, `{"invite":"`+token+`"}`)
	if loc.Path != "/invite/"+token || loc.Query().Get("oauth_error") != "invite_email_mismatch" {
		t.Fatalf("sent to %s, want back to the invitation with a mismatch", loc)
	}
	if e.signedIn(c) != nil {
		t.Error("signed in on somebody else's invitation")
	}
	// The invitation is still good for the person it was written for.
	if _, err := e.accounts.InvitationByToken(context.Background(), auth.HashSessionToken(token)); err != nil {
		t.Error("a refused attempt used the invitation up")
	}
}

// The address matches, but the provider does not vouch for it: whoever made
// that provider account may have typed somebody else's address.
func TestOAuthRefusesAnUnverifiedAddressEvenOnTheInvitation(t *testing.T) {
	e := newOAuthEnv(t)
	token := e.invite("Acme", "ann@acme.test")
	e.idp.as("g-claims-ann", "ann@acme.test", false)

	loc := e.signInWithGoogle(newClient(t, e.h), `{"invite":"`+token+`"}`)
	if loc.Query().Get("oauth_error") != "invite_email_mismatch" {
		t.Fatalf("an unverified address redeemed an invitation: %s", loc)
	}
}

// Somebody who registered with a password can later just press "Continue
// with Google": a verified matching address connects the two.
func TestOAuthConnectsAnExistingAccountByVerifiedAddress(t *testing.T) {
	e := newOAuthEnv(t)
	owner, _ := signedInOwner(t, e.h, "Acme", "ann@acme.test")
	owner.do(http.MethodPost, "/api/auth/logout", "")
	e.idp.as("g-ann", "Ann@Acme.test", true) // case differs, and should not matter

	c := newClient(t, e.h)
	if loc := e.signInWithGoogle(c, `{"next":"/app/members"}`); loc.Path != "/app/members" {
		t.Fatalf("sent to %s, want where they were going", loc)
	}
	if me := e.signedIn(c); me == nil || me.User.Email != "ann@acme.test" {
		t.Fatalf("not signed in as Ann: %+v", me)
	}
	ids, _ := e.accounts.IdentitiesOf(context.Background(), e.userID("ann@acme.test"))
	if len(ids) != 1 || ids[0].Provider != "google" {
		t.Errorf("the identity was not connected: %+v", ids)
	}
}

func (e *oauthEnv) userID(email string) string {
	u, err := e.accounts.UserByEmail(context.Background(), email)
	if err != nil {
		e.t.Fatalf("no user %s", email)
	}
	return u.ID
}

func TestOAuthNeverConnectsOnAnUnverifiedAddress(t *testing.T) {
	e := newOAuthEnv(t)
	signedInOwner(t, e.h, "Acme", "ann@acme.test")
	e.idp.as("g-mallory", "ann@acme.test", false)

	c := newClient(t, e.h)
	loc := e.signInWithGoogle(c, `{}`)
	if loc.Path != "/login" || loc.Query().Get("oauth_error") != "email_unverified" {
		t.Fatalf("sent to %s, want a refusal", loc)
	}
	if e.signedIn(c) != nil {
		t.Fatal("an unverified address signed in to somebody's account")
	}
	if ids, _ := e.accounts.IdentitiesOf(context.Background(), e.userID("ann@acme.test")); len(ids) != 0 {
		t.Error("an unverified address was connected to the account anyway")
	}
}

// Registration is closed whichever way somebody arrives.
func TestOAuthIsNotAWayToRegister(t *testing.T) {
	e := newOAuthEnv(t)
	e.idp.as("g-stranger", "stranger@example.test", true)
	c := newClient(t, e.h)
	loc := e.signInWithGoogle(c, `{}`)
	if loc.Query().Get("oauth_error") != "no_account" || e.signedIn(c) != nil {
		t.Fatalf("a stranger got in through Google: %s", loc)
	}
	if _, err := e.accounts.UserByEmail(context.Background(), "stranger@example.test"); err == nil {
		t.Error("an account was created for a stranger")
	}
}

func TestOAuthCallbackWithoutItsStateIsRefused(t *testing.T) {
	e := newOAuthEnv(t)
	signedInOwner(t, e.h, "Acme", "ann@acme.test")
	e.idp.as("g-ann", "ann@acme.test", true)

	// A callback that this browser did not start — the login-CSRF case,
	// where somebody else's code is completed in the victim's browser.
	c := newClient(t, e.h)
	rec := c.do(http.MethodGet, "/api/auth/oauth/google/callback?code=good-code&state=made-up", "")
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("oauth_error") != "state" || e.signedIn(c) != nil {
		t.Fatalf("a callback without its state was accepted: %s", loc)
	}
}

func TestOAuthNextCannotLeaveTheConsole(t *testing.T) {
	for _, next := range []string{"https://evil.example", "//evil.example/app", "/login", "/app\\evil"} {
		if got := safeNext(next); got != "/app" {
			t.Errorf("safeNext(%q) = %q, want /app", next, got)
		}
	}
	if got := safeNext("/app/members"); got != "/app/members" {
		t.Errorf("a console path was not kept: %q", got)
	}
}

// Connecting from the account page links to whoever is signed in, whatever the
// provider's address — they have just proved they hold both.
func TestOAuthLinkingConnectsToTheSignedInPerson(t *testing.T) {
	e := newOAuthEnv(t)
	ann, _ := signedInOwner(t, e.h, "Acme", "ann@acme.test")
	e.idp.as("g-personal", "ann.personal@gmail.example", false)

	loc := e.signInWithGoogle(ann, `{"link":true}`)
	if loc.Path != "/app/account" || loc.Query().Get("connected") != "google" {
		t.Fatalf("sent to %s, want the account page", loc)
	}
	rec := ann.do(http.MethodGet, "/api/auth/identities", "")
	var got struct {
		Identities  []storage.Identity `json:"identities"`
		HasPassword bool               `json:"has_password"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Identities) != 1 || !got.HasPassword {
		t.Fatalf("identities = %+v", got)
	}
}

func TestOAuthLinkingRefusesAnIdentitySomeoneElseHas(t *testing.T) {
	e := newOAuthEnv(t)
	ann, _ := signedInOwner(t, e.h, "Acme", "ann@acme.test")
	bob, _ := signedInOwner(t, e.h, "Globex", "bob@globex.test")
	e.idp.as("g-shared", "shared@example.test", true)

	e.signInWithGoogle(ann, `{"link":true}`)
	loc := e.signInWithGoogle(bob, `{"link":true}`)
	if loc.Query().Get("oauth_error") != "identity_taken" {
		t.Fatalf("one Google account was connected to two people: %s", loc)
	}
}

func TestOAuthLinkNeedsASession(t *testing.T) {
	e := newOAuthEnv(t)
	rec := newClient(t, e.h).do(http.MethodPost, "/api/auth/oauth/google/start", `{"link":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("linking without a session = %d, want 401", rec.Code)
	}
}

// The last way to sign in cannot be removed.
func TestUnlinkingTheOnlyWayInIsRefused(t *testing.T) {
	e := newOAuthEnv(t)
	token := e.invite("Acme", "ann@acme.test")
	e.idp.as("g-ann", "ann@acme.test", true)
	c := newClient(t, e.h)
	e.signInWithGoogle(c, `{"invite":"`+token+`"}`)

	ids, _ := e.accounts.IdentitiesOf(context.Background(), e.userID("ann@acme.test"))
	rec := c.do(http.MethodDelete, "/api/auth/identities/"+ids[0].ID, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("removing the only way in = %d: %s", rec.Code, rec.Body.String())
	}

	// With a second one connected, the first can go.
	e.idp.as("g-ann-2", "ann2@example.test", false)
	e.signInWithGoogle(c, `{"link":true}`)
	if rec := c.do(http.MethodDelete, "/api/auth/identities/"+ids[0].ID, ""); rec.Code != http.StatusNoContent {
		t.Errorf("removing one of two = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProvidersListsOnlyWhatIsConfigured(t *testing.T) {
	e := newOAuthEnv(t)
	rec := newClient(t, e.h).do(http.MethodGet, "/api/auth/providers", "")
	var out struct {
		Providers []struct{ ID, Name string } `json:"providers"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Providers) != 1 || out.Providers[0].ID != "google" {
		t.Errorf("providers = %+v", out.Providers)
	}
}
