package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/storage"
)

// client keeps cookies between calls, the way a browser does — half of what
// is being tested here is cookie behaviour.
type client struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	// headers are sent with every request, for tests about what a caller can
	// influence by sending one.
	headers map[string]string
	// orgID is the organization this client signed in to, remembered so tests
	// can name the other one.
	orgID string
}

func newClient(t *testing.T, h http.Handler) *client {
	return &client{t: t, h: h, cookies: map[string]string{}}
}

func (c *client) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for name, value := range c.cookies {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	for name, value := range c.headers {
		r.Header.Set(name, value)
	}
	// A browser would read the CSRF cookie and echo it; so does this.
	if csrf, ok := c.cookies[csrfCookie]; ok {
		r.Header.Set(auth.CSRFHeader, csrf)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	for _, cookie := range rec.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
			continue
		}
		c.cookies[cookie.Name] = cookie.Value
	}
	return rec
}

func (c *client) me(rec *httptest.ResponseRecorder) meResponse {
	c.t.Helper()
	var out meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		c.t.Fatalf("not a me response: %s", rec.Body.String())
	}
	return out
}

func accountsRouter(t *testing.T) (http.Handler, storage.AccountStore) {
	t.Helper()
	accounts := storage.NewMemAccountStore()
	h := Routes(Options{
		KeyStore:     config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore: accounts,
		AdminAPIKey:  "instance-admin",
		Logger:       slog.Default(),
	})
	return h, accounts
}

// bootstrap does what an operator does on a fresh installation: create the
// organization and get the owner's invitation link.
func bootstrap(t *testing.T, h http.Handler, name, ownerEmail string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/instance/organizations",
		strings.NewReader(`{"name":"`+name+`","owner_email":"`+ownerEmail+`"}`))
	req.Header.Set("Authorization", "Bearer instance-admin")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Invitation struct {
			Token string `json:"token"`
			Link  string `json:"link"`
		} `json:"invitation"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Invitation.Token == "" || !strings.Contains(out.Invitation.Link, out.Invitation.Token) {
		t.Fatalf("bootstrap returned no usable invitation: %s", rec.Body.String())
	}
	return out.Invitation.Token
}

func TestRegisterThroughInvitationAndSignIn(t *testing.T) {
	h, _ := accountsRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")

	c := newClient(t, h)
	rec := c.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", rec.Code, rec.Body.String())
	}
	me := c.me(rec)
	if me.User.Email != "owner@acme.test" || me.Role != storage.RoleOwner {
		t.Fatalf("registered as %+v, want the owner of the organization", me)
	}
	if me.Organization == nil || me.Organization.Slug != "acme" {
		t.Errorf("organization = %+v, want the one from the invitation", me.Organization)
	}
	if c.cookies[auth.SessionCookie] == "" || c.cookies[csrfCookie] == "" {
		t.Error("registration did not set both cookies")
	}
	// The password must never travel back.
	if strings.Contains(rec.Body.String(), "argon2") || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("the response mentions the password: %s", rec.Body.String())
	}

	// The same invitation cannot be used twice.
	again := newClient(t, h).do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Impostor","password":"another long password"}`)
	if again.Code != http.StatusForbidden && again.Code != http.StatusConflict {
		t.Errorf("reused invitation = %d, want it refused: %s", again.Code, again.Body.String())
	}

	// Sign out, then back in.
	if rec := c.do(http.MethodPost, "/api/auth/logout", ""); rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("me after logout = %d, want 401", rec.Code)
	}
	rec = c.do(http.MethodPost, "/api/auth/login",
		`{"email":"owner@acme.test","password":"a good long password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body.String())
	}
	if me := c.me(rec); me.Role != storage.RoleOwner {
		t.Errorf("signed in as %+v", me)
	}
}

// An invitation names one address. A leaked link must not let anybody else in.
func TestInvitationIsBoundToItsAddress(t *testing.T) {
	h, _ := accountsRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")

	rec := newClient(t, h).do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"someone.else@evil.test","name":"Nope","password":"a good long password"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("register with another address = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestRegistrationIsClosedWithoutAnInvitation(t *testing.T) {
	h, _ := accountsRouter(t)
	for _, body := range []string{
		`{"email":"stranger@acme.test","password":"a good long password"}`,
		`{"token":"not-a-real-token","email":"stranger@acme.test","password":"a good long password"}`,
	} {
		rec := newClient(t, h).do(http.MethodPost, "/api/auth/register", body)
		if rec.Code == http.StatusOK {
			t.Errorf("registered without a valid invitation: %s", body)
		}
	}
}

// Wrong password and unknown address must be indistinguishable in the answer.
func TestLoginRefusalsLookTheSame(t *testing.T) {
	h, _ := accountsRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")
	newClient(t, h).do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)

	wrong := newClient(t, h).do(http.MethodPost, "/api/auth/login",
		`{"email":"owner@acme.test","password":"not the password"}`)
	missing := newClient(t, h).do(http.MethodPost, "/api/auth/login",
		`{"email":"nobody@acme.test","password":"not the password"}`)

	if wrong.Code != http.StatusUnauthorized || missing.Code != http.StatusUnauthorized {
		t.Fatalf("codes: wrong=%d missing=%d", wrong.Code, missing.Code)
	}
	if wrong.Body.String() != missing.Body.String() {
		t.Errorf("the answers differ, which tells a stranger the address exists:\n  %s\n  %s",
			wrong.Body.String(), missing.Body.String())
	}
}

func TestLoginAttemptsAreLimited(t *testing.T) {
	h, _ := accountsRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")
	newClient(t, h).do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)

	c := newClient(t, h)
	body := `{"email":"owner@acme.test","password":"guess"}`
	for i := 0; i < maxLoginAttempts; i++ {
		c.do(http.MethodPost, "/api/auth/login", body)
	}
	rec := c.do(http.MethodPost, "/api/auth/login", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d = %d, want 429", maxLoginAttempts+1, rec.Code)
	}
	// Even the right password is refused while the limit holds — otherwise
	// the limiter is a hint about which guess was close.
	if rec := c.do(http.MethodPost, "/api/auth/login",
		`{"email":"owner@acme.test","password":"a good long password"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the limit lifted for the right password: %d", rec.Code)
	}
}

// A cookie-authenticated write without the CSRF header is what a cross-site
// form post looks like.
func TestCSRFProtectsCookieAuthenticatedWrites(t *testing.T) {
	h, _ := accountsRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")
	c := newClient(t, h)
	c.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/invitations",
		strings.NewReader(`{"email":"mate@acme.test","role":"member"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.cookies[auth.SessionCookie]})
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: c.cookies[csrfCookie]})
	// No X-Aperture-CSRF header on purpose.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write without the CSRF header = %d, want 403", rec.Code)
	}

	// Reading is allowed without it: a GET changes nothing.
	if rec := c.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusOK {
		t.Errorf("GET /me = %d", rec.Code)
	}
}

func TestRolesGovernInvitations(t *testing.T) {
	h, _ := accountsRouter(t)
	ownerToken := bootstrap(t, h, "Acme", "owner@acme.test")
	owner := newClient(t, h)
	owner.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+ownerToken+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)

	// The owner invites a viewer.
	rec := owner.do(http.MethodPost, "/api/invitations", `{"email":"viewer@acme.test","role":"viewer"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)

	viewer := newClient(t, h)
	if rec := viewer.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"viewer@acme.test","name":"Viewer","password":"a good long password"}`); rec.Code != http.StatusOK {
		t.Fatalf("viewer register = %d: %s", rec.Code, rec.Body.String())
	}

	// A viewer may look at the members list but may not invite anybody.
	if rec := viewer.do(http.MethodGet, "/api/members", ""); rec.Code != http.StatusOK {
		t.Errorf("viewer reading members = %d, want 200", rec.Code)
	}
	if rec := viewer.do(http.MethodPost, "/api/invitations",
		`{"email":"another@acme.test","role":"member"}`); rec.Code != http.StatusForbidden {
		t.Errorf("viewer inviting = %d, want 403", rec.Code)
	}
}

// Nobody may hand out more rights than they hold.
func TestAdminCannotInviteAnOwner(t *testing.T) {
	h, accounts := accountsRouter(t)
	ownerToken := bootstrap(t, h, "Acme", "owner@acme.test")
	owner := newClient(t, h)
	owner.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+ownerToken+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)

	rec := owner.do(http.MethodPost, "/api/invitations", `{"email":"admin@acme.test","role":"admin"}`)
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)
	admin := newClient(t, h)
	admin.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"admin@acme.test","name":"Admin","password":"a good long password"}`)

	if rec := admin.do(http.MethodPost, "/api/invitations",
		`{"email":"usurper@acme.test","role":"owner"}`); rec.Code != http.StatusForbidden {
		t.Errorf("admin inviting an owner = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	_ = accounts
}

// An organization must never be left without an owner.
func TestLastOwnerCannotBeDemotedOrRemoved(t *testing.T) {
	h, _ := accountsRouter(t)
	ownerToken := bootstrap(t, h, "Acme", "owner@acme.test")
	owner := newClient(t, h)
	rec := owner.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+ownerToken+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)
	me := owner.me(rec)

	if rec := owner.do(http.MethodPut, "/api/members/"+me.User.ID+"/role", `{"role":"member"}`); rec.Code != http.StatusConflict {
		t.Errorf("demoting the last owner = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if rec := owner.do(http.MethodDelete, "/api/members/"+me.User.ID, ""); rec.Code != http.StatusConflict {
		t.Errorf("removing the last owner = %d, want 409", rec.Code)
	}
}

// Two organizations, one person in each: neither session may see the other.
func TestSessionsAreBoundToTheirOrganization(t *testing.T) {
	h, _ := accountsRouter(t)
	acmeToken := bootstrap(t, h, "Acme", "owner@acme.test")
	otherToken := bootstrap(t, h, "Other", "owner@other.test")

	acme := newClient(t, h)
	acmeMe := acme.me(acme.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+acmeToken+`","email":"owner@acme.test","name":"A","password":"a good long password"}`))
	other := newClient(t, h)
	otherMe := other.me(other.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+otherToken+`","email":"owner@other.test","name":"B","password":"a good long password"}`))

	if acmeMe.Organization.ID == otherMe.Organization.ID {
		t.Fatal("both registrations landed in the same organization")
	}
	// Switching into somebody else's organization is refused.
	if rec := acme.do(http.MethodPost, "/api/auth/switch-org",
		`{"org_id":"`+otherMe.Organization.ID+`"}`); rec.Code != http.StatusForbidden {
		t.Errorf("switching into a stranger's organization = %d, want 403", rec.Code)
	}
	// And each sees only their own members.
	var list struct {
		Members []storage.Member `json:"members"`
	}
	json.Unmarshal(acme.do(http.MethodGet, "/api/members", "").Body.Bytes(), &list)
	if len(list.Members) != 1 || list.Members[0].Email != "owner@acme.test" {
		t.Errorf("acme sees %+v", list.Members)
	}
}

// Signing out everywhere must kill the other browser too.
func TestLogoutEverywhere(t *testing.T) {
	h, _ := accountsRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")
	first := newClient(t, h)
	first.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)

	second := newClient(t, h)
	second.do(http.MethodPost, "/api/auth/login",
		`{"email":"owner@acme.test","password":"a good long password"}`)
	if rec := second.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusOK {
		t.Fatalf("second session = %d", rec.Code)
	}

	first.do(http.MethodPost, "/api/auth/logout?everywhere=true", "")
	if rec := second.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the other session survived = %d, want 401", rec.Code)
	}
}

// Without an account store the gateway still serves agents; the sign-in
// endpoints say so instead of pretending.
func TestWithoutAccountsAuthEndpointsAreUnavailable(t *testing.T) {
	h := Routes(Options{
		KeyStore:    config.NewRuntimeStore("ap-test").KeyStore(),
		AdminAPIKey: "instance-admin",
		Logger:      slog.Default(),
	})
	c := newClient(t, h)
	if rec := c.do(http.MethodPost, "/api/auth/login", `{"email":"a@b.co","password":"x"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("login without accounts = %d, want 503", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("me without accounts = %d, want 503", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/health", ""); rec.Code != http.StatusOK {
		t.Errorf("health without accounts = %d, want 200", rec.Code)
	}
}
