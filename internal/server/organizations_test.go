package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mutegate/mutegate/internal/alerter"
	"github.com/mutegate/mutegate/internal/config"
	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/storage"
)

// A gateway with accounts and enough of the traffic stores that a service
// token has something to read.
func orgRouter(t *testing.T) (http.Handler, storage.AccountStore, *storage.MemDLPStore) {
	t.Helper()
	accounts := storage.NewMemAccountStore()
	dlp := storage.NewMemDLPStore(100)
	h := Routes(Options{
		KeyStore:     config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore: accounts,
		DLPStore:     dlp,
		PolicyStore:  storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionBlock}),
		AdminAPIKey:  "instance-admin",
		Logger:       slog.Default(),
	})
	return h, accounts, dlp
}

// signedInOwner bootstraps an organization and returns its owner's browser
// and the organization id.
func signedInOwner(t *testing.T, h http.Handler, name, email string) (*client, string) {
	t.Helper()
	token := bootstrap(t, h, name, email)
	c := newClient(t, h)
	rec := c.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"`+email+`","name":"Owner","password":"a good long password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("register in %s = %d: %s", name, rec.Code, rec.Body.String())
	}
	me := c.me(rec)
	if me.Organization == nil {
		t.Fatalf("no organization after registering in %s", name)
	}
	c.orgID = me.Organization.ID
	return c, me.Organization.ID
}

// mintToken creates a service token through the API, the way an admin would.
func mintToken(t *testing.T, c *client, name string, scopes ...string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "scopes": scopes})
	rec := c.do(http.MethodPost, "/api/tokens", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token = %d: %s", rec.Code, rec.Body.String())
	}
	var out tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("token response: %s", rec.Body.String())
	}
	if out.Token == "" || !strings.HasPrefix(out.Token, serviceTokenPrefix) {
		t.Fatalf("token does not look like one: %q", out.Token)
	}
	return out.Token
}

// asToken sends a request the way CI would: a bearer token and no cookies.
func asToken(t *testing.T, h http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func seedIncident(t *testing.T, dlp *storage.MemDLPStore, orgID, rule string) {
	t.Helper()
	err := dlp.Insert(context.Background(), storage.DLPEvent{
		OrgID: orgID, Ts: time.Now(), KeyID: "k", Model: "gpt-4o-mini", Provider: "openai",
		Rule: rule, Group: "secrets", Action: "blocked", MaskedSample: "***",
	})
	if err != nil {
		t.Fatalf("seed an incident: %v", err)
	}
}

// A service token is a credential for a machine: it belongs to one
// organization, and its scopes say what it may do there.
func TestServiceTokenReadsWhatItsScopesAllow(t *testing.T) {
	h, _, dlp := orgRouter(t)
	owner, orgID := signedInOwner(t, h, "Acme", "owner@acme.test")
	seedIncident(t, dlp, orgID, "acme-rule")

	token := mintToken(t, owner, "ci", string(storage.ScopeEventsRead))

	rec := asToken(t, h, token, http.MethodGet, "/admin/dlp/events", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("events with events:read = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "acme-rule") {
		t.Errorf("the token did not see its organization's incidents: %s", rec.Body.String())
	}

	// The same token on an endpoint it has no scope for.
	rec = asToken(t, h, token, http.MethodPost, "/admin/keys", `{"name":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a read-only token created a key: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), string(storage.ScopeKeysWrite)) {
		t.Errorf("the refusal does not say which scope is missing: %s", rec.Body.String())
	}
}

// Scopes are not the only gate: an endpoint that is not in the table cannot be
// reached with a token at all, whatever scopes it carries.
func TestServiceTokenCannotReachPeopleEndpoints(t *testing.T) {
	h, _, _ := orgRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")
	token := mintToken(t, owner, "ci",
		string(storage.ScopeEventsRead), string(storage.ScopeKeysWrite),
		string(storage.ScopePoliciesWrite))

	for _, path := range []string{"/api/members", "/api/invitations", "/api/tokens"} {
		rec := asToken(t, h, token, http.MethodGet, path, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s answered a service token with %d: %s", path, rec.Code, rec.Body.String())
		}
		// And says why, rather than "not signed in", which would send
		// somebody looking for a session they never had.
		if !strings.Contains(rec.Body.String(), "service tokens") {
			t.Errorf("%s does not explain the refusal: %s", path, rec.Body.String())
		}
	}
	// And the one that would let a token mint itself more tokens.
	rec := asToken(t, h, token, http.MethodPost, "/api/tokens", `{"name":"another","scopes":["events:read"]}`)
	if rec.Code == http.StatusCreated {
		t.Error("a service token minted another service token")
	}
}

func TestServiceTokenIsBoundToItsOrganization(t *testing.T) {
	h, _, dlp := orgRouter(t)
	acme, acmeID := signedInOwner(t, h, "Acme", "owner@acme.test")
	_, globexID := signedInOwner(t, h, "Globex", "owner@globex.test")
	seedIncident(t, dlp, acmeID, "acme-rule")
	seedIncident(t, dlp, globexID, "globex-rule")

	token := mintToken(t, acme, "ci", string(storage.ScopeEventsRead))

	rec := asToken(t, h, token, http.MethodGet, "/admin/dlp/events", "")
	body := rec.Body.String()
	if !strings.Contains(body, "acme-rule") || strings.Contains(body, "globex-rule") {
		t.Fatalf("a token saw the wrong organization's incidents: %s", body)
	}
	// Naming another organization does not change the answer: the token's own
	// organization is the only one it has.
	r := httptest.NewRequest(http.MethodGet, "/admin/dlp/events", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Mutegate-Org", globexID)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if strings.Contains(rec.Body.String(), "globex-rule") {
		t.Errorf("a header moved a service token to another organization: %s", rec.Body.String())
	}
}

func TestRevokedServiceTokenStopsWorking(t *testing.T) {
	h, _, _ := orgRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")
	token := mintToken(t, owner, "ci", string(storage.ScopeEventsRead))

	if rec := asToken(t, h, token, http.MethodGet, "/admin/dlp/events", ""); rec.Code != http.StatusOK {
		t.Fatalf("token does not work to begin with: %d", rec.Code)
	}

	list := owner.do(http.MethodGet, "/api/tokens", "")
	var listed struct {
		Tokens []storage.ServiceToken `json:"tokens"`
	}
	json.Unmarshal(list.Body.Bytes(), &listed)
	if len(listed.Tokens) != 1 {
		t.Fatalf("listed %d tokens, want 1: %s", len(listed.Tokens), list.Body.String())
	}
	if strings.Contains(list.Body.String(), token) {
		t.Error("the full token is readable after creation; only the hint should be")
	}

	if rec := owner.do(http.MethodDelete, "/api/tokens/"+listed.Tokens[0].ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := asToken(t, h, token, http.MethodGet, "/admin/dlp/events", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked token still works: %d %s", rec.Code, rec.Body.String())
	}
}

func TestServiceTokenNeedsAtLeastOneScope(t *testing.T) {
	h, _, _ := orgRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")

	rec := owner.do(http.MethodPost, "/api/tokens", `{"name":"ci","scopes":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a token with no scopes was created: %d %s", rec.Code, rec.Body.String())
	}
	rec = owner.do(http.MethodPost, "/api/tokens", `{"name":"ci","scopes":["everything"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown scope was accepted: %d %s", rec.Code, rec.Body.String())
	}
	rec = owner.do(http.MethodPost, "/api/tokens", `{"name":"","scopes":["events:read"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a nameless token was created: %d %s", rec.Code, rec.Body.String())
	}
}

// Registration redeems an invitation for somebody new. Somebody who already
// has an account needs the same door, or being invited to a second
// organization is an invitation to a wall.
func TestExistingUserJoinsASecondOrganization(t *testing.T) {
	h, _, _ := orgRouter(t)
	acme, acmeID := signedInOwner(t, h, "Acme", "person@example.test")
	globex, globexID := signedInOwner(t, h, "Globex", "owner@globex.test")

	// Globex invites somebody who already has an Acme account.
	rec := globex.do(http.MethodPost, "/api/invitations",
		`{"email":"person@example.test","role":"member"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)

	// Registering again is refused — the account exists.
	rec = acme.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"person@example.test","name":"Person","password":"a good long password"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("registering over an existing account = %d, want 409", rec.Code)
	}

	// Accepting while signed in is the way in.
	rec = acme.do(http.MethodPost, "/api/invitations/accept", `{"token":"`+inv.Token+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", rec.Code, rec.Body.String())
	}
	me := acme.me(rec)
	if me.Organization == nil || me.Organization.ID != globexID {
		t.Fatalf("accepting did not land them in the new organization: %+v", me.Organization)
	}
	if me.Role != storage.RoleMember {
		t.Errorf("role after joining = %q, want member", me.Role)
	}
	if len(me.Organizations) != 2 {
		t.Fatalf("they belong to %d organizations, want 2", len(me.Organizations))
	}

	// The invitation is single use.
	if rec := acme.do(http.MethodPost, "/api/invitations/accept", `{"token":"`+inv.Token+`"}`); rec.Code == http.StatusOK {
		t.Error("an invitation was accepted twice")
	}

	// And switching back works, now that there is something to switch to.
	rec = acme.do(http.MethodPost, "/api/auth/switch-org", `{"org_id":"`+acmeID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch back = %d: %s", rec.Code, rec.Body.String())
	}
	if me := acme.me(rec); me.Organization == nil || me.Organization.ID != acmeID {
		t.Errorf("switching back did not work: %+v", me.Organization)
	}
}

func TestInvitationCannotBeAcceptedByTheWrongAccount(t *testing.T) {
	h, _, _ := orgRouter(t)
	acme, _ := signedInOwner(t, h, "Acme", "owner@acme.test")
	globex, _ := signedInOwner(t, h, "Globex", "owner@globex.test")

	rec := globex.do(http.MethodPost, "/api/invitations",
		`{"email":"somebody-else@example.test","role":"member"}`)
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)

	if rec := acme.do(http.MethodPost, "/api/invitations/accept", `{"token":"`+inv.Token+`"}`); rec.Code != http.StatusForbidden {
		t.Errorf("somebody else's invitation was accepted: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLeavingAnOrganization(t *testing.T) {
	h, _, _ := orgRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")

	// The last owner may not leave: that would strand the organization.
	rec := owner.do(http.MethodPost, "/api/organizations/current/leave", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("the last owner left = %d: %s", rec.Code, rec.Body.String())
	}

	// With somebody else in the organization, a member may go.
	inviteRec := owner.do(http.MethodPost, "/api/invitations", `{"email":"member@acme.test","role":"member"}`)
	var inv inviteResponse
	json.Unmarshal(inviteRec.Body.Bytes(), &inv)
	member := newClient(t, h)
	regRec := member.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"member@acme.test","name":"Member","password":"a good long password"}`)
	if regRec.Code != http.StatusOK {
		t.Fatalf("register the member = %d: %s", regRec.Code, regRec.Body.String())
	}

	rec = member.do(http.MethodPost, "/api/organizations/current/leave", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("member leaving = %d: %s", rec.Code, rec.Body.String())
	}
	// It was their only organization, so they are signed out.
	if rec := member.do(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("still signed in after leaving their only organization: %d", rec.Code)
	}
	// And they are gone from the members list.
	list := owner.do(http.MethodGet, "/api/members", "")
	if strings.Contains(list.Body.String(), "member@acme.test") {
		t.Errorf("a member who left is still listed: %s", list.Body.String())
	}
}

// Deleting an organization is soft, and it closes every door until somebody
// restores it: the console, the members, the service tokens.
func TestDeletingAnOrganizationClosesItAndRestoreReopensIt(t *testing.T) {
	h, accounts, dlp := orgRouter(t)
	owner, orgID := signedInOwner(t, h, "Acme", "owner@acme.test")
	seedIncident(t, dlp, orgID, "acme-rule")
	token := mintToken(t, owner, "ci", string(storage.ScopeEventsRead))

	org, err := accounts.OrganizationByID(context.Background(), orgID)
	if err != nil {
		t.Fatal(err)
	}

	// Naming the wrong organization does not delete anything.
	rec := owner.do(http.MethodDelete, "/api/organizations/current", `{"slug":"not-the-one"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("deletion without confirming the slug = %d: %s", rec.Code, rec.Body.String())
	}

	rec = owner.do(http.MethodDelete, "/api/organizations/current", `{"slug":"`+org.Slug+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}

	// The console: the owner is signed in but in no organization.
	me := owner.me(owner.do(http.MethodGet, "/api/auth/me", ""))
	if me.Organization != nil {
		t.Errorf("a deleted organization is still the session's: %+v", me.Organization)
	}
	if len(me.Organizations) != 0 {
		t.Errorf("a deleted organization is still offered: %d", len(me.Organizations))
	}
	if rec := owner.do(http.MethodGet, "/admin/dlp/events", ""); rec.Code != http.StatusForbidden {
		t.Errorf("the incident feed still answers a deleted organization: %d", rec.Code)
	}
	// The service token: gone with everything else.
	if rec := asToken(t, h, token, http.MethodGet, "/admin/dlp/events", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a deleted organization's token still works: %d %s", rec.Code, rec.Body.String())
	}

	// The operator restores it, because nobody inside can.
	req := httptest.NewRequest(http.MethodPost, "/api/instance/organizations/"+orgID+"/restore", nil)
	req.Header.Set("Authorization", "Bearer instance-admin")
	restore := httptest.NewRecorder()
	h.ServeHTTP(restore, req)
	if restore.Code != http.StatusOK {
		t.Fatalf("restore = %d: %s", restore.Code, restore.Body.String())
	}

	me = owner.me(owner.do(http.MethodGet, "/api/auth/me", ""))
	if me.Organization == nil || me.Organization.ID != orgID {
		t.Fatalf("restore did not bring the organization back: %+v", me.Organization)
	}
	if rec := owner.do(http.MethodGet, "/admin/dlp/events", ""); rec.Code != http.StatusOK {
		t.Errorf("the feed did not come back: %d", rec.Code)
	}
	if rec := asToken(t, h, token, http.MethodGet, "/admin/dlp/events", ""); rec.Code != http.StatusOK {
		t.Errorf("the service token did not come back: %d", rec.Code)
	}
}

func TestRenamingAnOrganization(t *testing.T) {
	h, _, _ := orgRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")

	rec := owner.do(http.MethodPatch, "/api/organizations/current", `{"name":"Acme Corporation"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", rec.Code, rec.Body.String())
	}
	me := owner.me(owner.do(http.MethodGet, "/api/auth/me", ""))
	if me.Organization == nil || me.Organization.Name != "Acme Corporation" {
		t.Errorf("the new name is not what /me reports: %+v", me.Organization)
	}
	// The slug is what other things point at, so it does not move.
	if me.Organization.Slug != "acme" {
		t.Errorf("renaming changed the slug to %q", me.Organization.Slug)
	}

	if rec := owner.do(http.MethodPatch, "/api/organizations/current", `{"name":"   "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty name was accepted: %d", rec.Code)
	}
}

// scopeByRoute is keyed by the mux's own pattern strings, which means a route
// renamed in routes.go and not here would silently stop being reachable by
// service tokens — or, worse, a new admin route would be reachable by none of
// them and nobody would notice until CI broke. So the table is checked against
// the routes themselves.
func TestScopeTableMatchesTheRoutes(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	registered := map[string]bool{}
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		registered[m[1]] = true
	}
	if len(registered) == 0 {
		t.Fatal("no routes found; this test is looking in the wrong place")
	}

	for pattern := range scopeByRoute {
		if !registered[pattern] {
			t.Errorf("scopeByRoute names %q, which is not a route any more: "+
				"service tokens would be refused everywhere that pattern was meant to cover", pattern)
		}
	}

	// The other direction is a judgement call rather than an error: most
	// admin routes should be reachable by a token, so a new one that is not
	// listed is worth a look, even though people-only endpoints belong
	// exactly where they are.
	var unlisted []string
	for pattern := range registered {
		if !strings.Contains(pattern, "/admin/") {
			continue
		}
		if _, ok := scopeByRoute[pattern]; !ok {
			unlisted = append(unlisted, pattern)
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Logf("admin routes no service token can reach (fine if deliberate): %s",
			strings.Join(unlisted, ", "))
	}
}

// The invite page shows what an invitation is for before asking for a
// password, and looking does not use the invitation up.
func TestInvitationLookupDescribesWithoutConsuming(t *testing.T) {
	h, _, _ := orgRouter(t)
	token := bootstrap(t, h, "Acme", "owner@acme.test")

	anon := newClient(t, h)
	rec := anon.do(http.MethodPost, "/api/invitations/lookup", `{"token":"`+token+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Organization  struct{ Name, Slug string } `json:"organization"`
		Email         string                      `json:"email"`
		Role          string                      `json:"role"`
		AccountExists bool                        `json:"account_exists"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Organization.Name != "Acme" || got.Email != "owner@acme.test" || got.Role != "owner" {
		t.Errorf("lookup described the wrong thing: %+v", got)
	}
	if got.AccountExists {
		t.Error("an address with no account was reported as having one")
	}

	// Looking twice is fine; the invitation still registers afterwards.
	anon.do(http.MethodPost, "/api/invitations/lookup", `{"token":"`+token+`"}`)
	reg := anon.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+token+`","email":"owner@acme.test","name":"Owner","password":"a good long password"}`)
	if reg.Code != http.StatusOK {
		t.Fatalf("looking used the invitation up: register = %d %s", reg.Code, reg.Body.String())
	}

	// Once used, it describes nothing — the same answer as a token that never
	// existed, so a probe learns nothing from the difference.
	used := newClient(t, h).do(http.MethodPost, "/api/invitations/lookup", `{"token":"`+token+`"}`)
	bogus := newClient(t, h).do(http.MethodPost, "/api/invitations/lookup",
		`{"token":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
	if used.Code != http.StatusNotFound || bogus.Code != http.StatusNotFound {
		t.Errorf("used = %d, bogus = %d; want 404 for both", used.Code, bogus.Code)
	}
	if used.Body.String() != bogus.Body.String() {
		t.Errorf("a used and an unknown invitation answer differently:\n%s\n%s", used.Body, bogus.Body)
	}
}

// A console served from another origin has to send its session cookie and
// the CSRF header; one that is not on the list gets nothing it can read.
func TestCORSCarriesCredentialsOnlyForAllowedOrigins(t *testing.T) {
	h := Routes(Options{
		KeyStore:       config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore:   storage.NewMemAccountStore(),
		AdminAPIKey:    "instance-admin",
		AllowedOrigins: []string{"http://localhost:5173"},
		Logger:         slog.Default(),
	})
	preflight := func(origin string) http.Header {
		r := httptest.NewRequest(http.MethodOptions, "/api/organizations/current", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "PATCH")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Header()
	}

	ok := preflight("http://localhost:5173")
	if ok.Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("an allowed origin cannot send its session cookie")
	}
	if !strings.Contains(ok.Get("Access-Control-Allow-Headers"), "X-Mutegate-CSRF") {
		t.Error("an allowed origin cannot send the CSRF header")
	}
	if !strings.Contains(ok.Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Error("an allowed origin cannot rename the organization")
	}

	other := preflight("https://evil.example")
	if other.Get("Access-Control-Allow-Origin") != "" || other.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("an origin not on the list got CORS headers: %v", other)
	}
}

// Registering is a sign-in; the members screen should not call someone who is
// looking at it "never signed in".
func TestRegistrationCountsAsASignIn(t *testing.T) {
	h, accounts, _ := orgRouter(t)
	_, _ = signedInOwner(t, h, "Acme", "owner@acme.test")
	u, err := accounts.UserByEmail(context.Background(), "owner@acme.test")
	if err != nil {
		t.Fatal(err)
	}
	if u.LastLoginAt == nil {
		t.Error("a freshly registered account has no sign-in recorded")
	}
}

// An installation upgraded from before multi-tenancy has all its data in the
// default organization, which a migration created and nobody was invited to.
// The operator has to be able to bring its first owner in, or the console
// that replaces the admin key would lock everybody out of their own data.
func TestOperatorInvitesTheFirstOwnerOfAnExistingOrganization(t *testing.T) {
	accounts := storage.NewMemAccountStore()
	h := Routes(Options{
		KeyStore:     config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore: accounts,
		AdminAPIKey:  "instance-admin",
		Logger:       slog.Default(),
	})
	org, err := accounts.CreateOrganization(context.Background(), "Default", "default")
	if err != nil {
		t.Fatal(err)
	}

	invite := func(key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/instance/organizations/"+org.ID+"/invitations", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	if rec := invite("", `{"email":"ops@example.test"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without the operator key = %d, want 401", rec.Code)
	}

	rec := invite("instance-admin", `{"email":"ops@example.test"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)
	if inv.Role != storage.RoleOwner || inv.OrgID != org.ID {
		t.Errorf("the invitation defaults to the wrong thing: role %q org %q", inv.Role, inv.OrgID)
	}

	c := newClient(t, h)
	reg := c.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"ops@example.test","name":"Ops","password":"a good long password"}`)
	if reg.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", reg.Code, reg.Body.String())
	}
	me := c.me(reg)
	if me.Organization == nil || me.Organization.ID != org.ID || me.Role != storage.RoleOwner {
		t.Errorf("the first owner did not land in the existing organization: %+v role %q", me.Organization, me.Role)
	}

	// A closed organization takes no one in.
	accounts.DeleteOrganization(context.Background(), org.ID)
	if rec := invite("instance-admin", `{"email":"late@example.test"}`); rec.Code != http.StatusNotFound {
		t.Errorf("inviting into a closed organization = %d, want 404", rec.Code)
	}
}

// An organization's admin sets the organization's own webhook; the
// environment's stays the default organization's, unseen by anyone else.
func TestAlertsAreTheOrganizations(t *testing.T) {
	accounts := storage.NewMemAccountStore()
	alerts := alerter.New(alerter.Config{URL: "https://hooks.slack.com/services/T0/B0/operator-secret", Format: alerter.FormatSlack}, nil).
		WithStore(storage.NewMemAlertStore())
	h := Routes(Options{
		KeyStore:     config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore: accounts,
		Alerter:      alerts,
		AdminAPIKey:  "instance-admin",
		Logger:       slog.Default(),
	})
	owner, orgID := signedInOwner(t, h, "Acme", "owner@acme.test")

	// What the operator's webhook is, Acme's owner does not get to see.
	rec := owner.do(http.MethodGet, "/admin/alerts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hooks.slack.com") {
		t.Errorf("an organization was shown the operator's webhook: %s", rec.Body.String())
	}

	rec = owner.do(http.MethodPut, "/admin/alerts",
		`{"url":"https://hooks.slack.com/services/T1/B1/acme-secret","format":"slack","actions":["blocked"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put = %d: %s", rec.Code, rec.Body.String())
	}
	mine, _ := alerts.ConfigFor(context.Background(), orgID)
	if mine.URL == "" || strings.Contains(mine.URL, "acme-secret") {
		t.Errorf("Acme's webhook, masked: %q", mine.URL)
	}
	operators, _ := alerts.ConfigFor(context.Background(), storage.DefaultOrgID)
	if operators.URL != "https://hooks.slack.com/****" {
		t.Errorf("Acme's save touched the default organization's webhook: %q", operators.URL)
	}

	// A viewer cannot redirect where the organization's incidents go.
	inv := owner.do(http.MethodPost, "/api/invitations", `{"email":"viewer@acme.test","role":"viewer"}`)
	var created inviteResponse
	json.Unmarshal(inv.Body.Bytes(), &created)
	viewer := newClient(t, h)
	viewer.do(http.MethodPost, "/api/auth/register",
		`{"token":"`+created.Token+`","email":"viewer@acme.test","name":"V","password":"a good long password"}`)
	if rec := viewer.do(http.MethodPut, "/admin/alerts", `{"url":"https://evil.example/hook","format":"json"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer changed the webhook: %d", rec.Code)
	}
}
