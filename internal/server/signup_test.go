package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danilovid/mutegate/internal/inspector"
	"github.com/danilovid/mutegate/internal/storage"
)

// signupRouter is a gateway with open registration, or not.
func signupRouter(t *testing.T, open bool) (http.Handler, *storage.MemAccountStore, *storage.MemAuditStore) {
	t.Helper()
	accounts := storage.NewMemAccountStore()
	journal := storage.NewMemAuditStore()
	h := Routes(Options{
		KeyStore:         &memKeys{},
		AccountStore:     accounts,
		AuditStore:       journal,
		PolicyStore:      storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionBlock}),
		AdminAPIKey:      "instance-admin",
		RegistrationOpen: open,
		Logger:           slog.Default(),
	})
	return h, accounts, journal
}

func signupBody(email, password, confirm string) string {
	b, _ := json.Marshal(map[string]string{"email": email, "password": password, "password_confirm": confirm})
	return string(b)
}

const goodPassword = "a good long password"

func registrationOffered(t *testing.T, h http.Handler) bool {
	t.Helper()
	rec := newClient(t, h).do(http.MethodGet, "/api/auth/providers", "")
	var out struct {
		Registration bool `json:"registration"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Registration
}

// Closed unless the operator opens it: an installation holding a company's
// provider keys does not become a public sign-up form by upgrading.
func TestRegistrationIsClosedByDefault(t *testing.T) {
	h, accounts, _ := signupRouter(t, false)
	rec := newClient(t, h).do(http.MethodPost, "/api/auth/signup", signupBody("ann@acme.test", goodPassword, goodPassword))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "invitation") {
		t.Errorf("sign-up on a closed installation = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := accounts.UserByEmail(context.Background(), "ann@acme.test"); err == nil {
		t.Error("a closed installation created an account")
	}
	if registrationOffered(t, h) {
		t.Error("the sign-in page is told registration is open")
	}
}

// The whole point: an address and a password, and a working console with an
// organization of one's own, owned.
func TestSigningUpGivesAnOrganizationOfOnesOwn(t *testing.T) {
	h, _, journal := signupRouter(t, true)
	if !registrationOffered(t, h) {
		t.Error("the sign-in page is not told registration is open")
	}

	ann := newClient(t, h)
	rec := ann.do(http.MethodPost, "/api/auth/signup", signupBody("Ann@Acme.test", goodPassword, goodPassword))
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-up = %d: %s", rec.Code, rec.Body.String())
	}
	me := ann.me(rec)
	if me.User.Email != "ann@acme.test" || me.Organization == nil || me.Organization.Name != "ann" ||
		me.Organization.Slug != "ann" || me.Role != storage.RoleOwner {
		t.Fatalf("after signing up: %+v in %+v as %q", me.User, me.Organization, me.Role)
	}

	// Signed in, and able to do what an owner does.
	if rec := ann.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusOK {
		t.Errorf("no session after signing up: %d", rec.Code)
	}
	if rec := ann.do(http.MethodPost, "/admin/keys", `{"name":"first"}`); rec.Code != http.StatusCreated {
		t.Errorf("the owner cannot create a key: %d %s", rec.Code, rec.Body.String())
	}

	// And the password works on the sign-in form from now on.
	rec = newClient(t, h).do(http.MethodPost, "/api/auth/login", `{"email":"ann@acme.test","password":"`+goodPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("sign-in with the new password = %d", rec.Code)
	}

	entries, _ := journal.List(context.Background(), me.Organization.ID, storage.AuditFilter{})
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
		if e.ActorLabel != "ann@acme.test" {
			t.Errorf("%s by %q, want the new account", e.Action, e.ActorLabel)
		}
	}
	if got := strings.Join(actions, " "); !strings.Contains(got, "member.join organization.create") {
		t.Errorf("journal = %v, want the organization's creation and its owner joining", actions)
	}
}

// Two people called Ann get two organizations, and neither sees the other's.
func TestTwoSignUpsAreTwoOrganizations(t *testing.T) {
	h, _, _ := signupRouter(t, true)
	first, second := newClient(t, h), newClient(t, h)
	a := first.me(first.do(http.MethodPost, "/api/auth/signup", signupBody("ann@acme.test", goodPassword, goodPassword)))
	b := second.me(second.do(http.MethodPost, "/api/auth/signup", signupBody("ann@globex.test", goodPassword, goodPassword)))
	if a.Organization == nil || b.Organization == nil || a.Organization.ID == b.Organization.ID {
		t.Fatalf("one organization for two sign-ups: %+v %+v", a.Organization, b.Organization)
	}
	if b.Organization.Slug != "ann-2" {
		t.Errorf("second Ann's slug = %q, want ann-2", b.Organization.Slug)
	}

	first.do(http.MethodPost, "/admin/keys", `{"name":"acme-only"}`)
	rec := second.do(http.MethodGet, "/admin/keys", "")
	if strings.Contains(rec.Body.String(), "acme-only") {
		t.Errorf("one sign-up sees another's keys: %s", rec.Body.String())
	}
	if rec := second.do(http.MethodGet, "/api/members", ""); strings.Contains(rec.Body.String(), "ann@acme.test") {
		t.Errorf("one sign-up sees another's members: %s", rec.Body.String())
	}
}

func TestSignUpRefusals(t *testing.T) {
	h, accounts, _ := signupRouter(t, true)
	existing := newClient(t, h)
	if rec := existing.do(http.MethodPost, "/api/auth/signup", signupBody("ann@acme.test", goodPassword, goodPassword)); rec.Code != http.StatusOK {
		t.Fatalf("first sign-up = %d", rec.Code)
	}

	cases := []struct {
		name, body string
		code       int
		says       string
	}{
		{"passwords differ", signupBody("bob@acme.test", goodPassword, goodPassword+"!"), http.StatusBadRequest, "do not match"},
		{"password too short", signupBody("bob@acme.test", "short", "short"), http.StatusBadRequest, "at least"},
		{"not an address", signupBody("bob", goodPassword, goodPassword), http.StatusBadRequest, "email"},
		{"address taken", signupBody("ANN@acme.test", "another long password", "another long password"), http.StatusConflict, "sign in instead"},
	}
	for _, c := range cases {
		rec := newClient(t, h).do(http.MethodPost, "/api/auth/signup", c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.says) {
			t.Errorf("%s: %d %s, want %d saying %q", c.name, rec.Code, rec.Body.String(), c.code, c.says)
		}
		if _, ok := rec.Result().Header["Set-Cookie"]; ok && rec.Code != http.StatusOK {
			t.Errorf("%s: a refused sign-up set a cookie", c.name)
		}
	}
	if _, err := accounts.UserByEmail(context.Background(), "bob@acme.test"); err == nil {
		t.Error("a refused sign-up created an account")
	}
	// Signing up over an existing address does not touch it: the old
	// password still works.
	rec := newClient(t, h).do(http.MethodPost, "/api/auth/login", `{"email":"ann@acme.test","password":"`+goodPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("the existing account's password stopped working: %d", rec.Code)
	}
}

// An open form is a way to fill the database and to ask which addresses have
// accounts, so one address gets a handful of tries an hour.
func TestSignUpsAreLimitedPerAddress(t *testing.T) {
	h, _, _ := signupRouter(t, true)
	try := func(ip string, i int) int {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/signup",
			strings.NewReader(signupBody("user"+strconv.Itoa(i)+"@acme.test", goodPassword, goodPassword)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < maxSignups; i++ {
		if code := try("203.0.113.9", i); code != http.StatusOK {
			t.Fatalf("sign-up %d = %d", i, code)
		}
	}
	if code := try("203.0.113.9", maxSignups); code != http.StatusTooManyRequests {
		t.Errorf("sign-up past the limit = %d, want 429", code)
	}
	if code := try("198.51.100.1", maxSignups+1); code != http.StatusOK {
		t.Errorf("another address was limited too: %d", code)
	}
}

func TestOrganizationNamesComeFromTheAddress(t *testing.T) {
	handlers := &Handlers{AccountStore: storage.NewMemAccountStore()}
	ctx := context.Background()
	cases := map[string]string{
		"Ann.Smith+ci@acme.test":          "ann-smith-ci",
		"___@acme.test":                   "org",
		strings.Repeat("x", 60) + "@a.io": strings.Repeat("x", 32),
	}
	for email, want := range cases {
		org, err := handlers.createOwnOrganization(ctx, strings.ToLower(email))
		if err != nil || org.Slug != want || !slugPattern.MatchString(org.Slug) {
			t.Errorf("%s: slug %q (%v), want %q", email, org.Slug, err, want)
		}
	}
	// Taken slugs are numbered, and then given a random tail.
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		org, err := handlers.createOwnOrganization(ctx, "dup@acme.test")
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if seen[org.Slug] || !slugPattern.MatchString(org.Slug) {
			t.Fatalf("attempt %d: slug %q reused or invalid", i, org.Slug)
		}
		seen[org.Slug] = true
	}
}
