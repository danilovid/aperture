package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danilovid/mutegate/internal/auth"
	"github.com/danilovid/mutegate/internal/storage"
)

// The project was called Aperture. What a browser, an agent or a script
// learned to send under that name keeps working: the new names are the ones
// written, the old ones are still read.

// legacyClient re-files a signed-in browser's cookies under their old names,
// as a browser signed in before the rename would carry them.
func legacyClient(t *testing.T, c *client) *client {
	t.Helper()
	old := newClient(t, c.h)
	old.cookies[legacySessionCookie] = c.cookies[auth.SessionCookie]
	old.cookies[legacyCSRFCookie] = c.cookies[csrfCookie]
	return old
}

// do for a legacy browser: cookies under the old names, the CSRF value echoed
// in the old header.
func (c *client) doLegacy(method, path, body string) *httptest.ResponseRecorder {
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
	if csrf, ok := c.cookies[legacyCSRFCookie]; ok {
		r.Header.Set("X-Aperture-CSRF", csrf)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	return rec
}

func TestASessionFromBeforeTheRenameStaysSignedIn(t *testing.T) {
	h, _, _ := signupRouter(t, true)
	c := newClient(t, h)
	if rec := c.do(http.MethodPost, "/api/auth/signup", signupBody("ann@acme.test", goodPassword, goodPassword)); rec.Code != http.StatusOK {
		t.Fatalf("sign-up = %d", rec.Code)
	}
	old := legacyClient(t, c)

	if rec := old.doLegacy(http.MethodGet, "/api/auth/me", ""); rec.Code != http.StatusOK {
		t.Errorf("a session under the old cookie name = %d, want signed in", rec.Code)
	}
	// Writes too: the old CSRF cookie and header still prove the request
	// came from the console.
	if rec := old.doLegacy(http.MethodPost, "/admin/keys", `{"name":"from-an-old-tab"}`); rec.Code != http.StatusCreated {
		t.Errorf("a write from an old session = %d: %s", rec.Code, rec.Body.String())
	}
	// And a header that does not match the cookie is still refused.
	req := httptest.NewRequest(http.MethodPost, "/admin/keys", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	for name, value := range old.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	req.Header.Set("X-Aperture-CSRF", "not-the-value")
	forged := httptest.NewRecorder()
	h.ServeHTTP(forged, req)
	if forged.Code != http.StatusForbidden {
		t.Errorf("a forged CSRF value under the old names = %d, want 403", forged.Code)
	}
}

// Signing in again drops the old cookies, so a browser never carries two
// sessions; signing out clears both names.
func TestSigningInReplacesTheOldCookies(t *testing.T) {
	h, _, _ := signupRouter(t, true)
	c := newClient(t, h)
	c.do(http.MethodPost, "/api/auth/signup", signupBody("ann@acme.test", goodPassword, goodPassword))
	c.cookies[legacySessionCookie] = "left-over"
	c.cookies[legacyCSRFCookie] = "left-over"

	rec := c.do(http.MethodPost, "/api/auth/login", `{"email":"ann@acme.test","password":"`+goodPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in = %d", rec.Code)
	}
	if _, ok := c.cookies[legacySessionCookie]; ok {
		t.Error("the old session cookie survived a new sign-in")
	}
	if _, ok := c.cookies[auth.SessionCookie]; !ok {
		t.Error("no session cookie under the new name")
	}
}

func TestOldHeaderNamesStillWork(t *testing.T) {
	// The operator naming an organization with X-Aperture-Org.
	h, _ := auditRouter(t)
	_, orgID := signedInOwner(t, h, "Acme", "owner@acme.test")
	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	req.Header.Set("Authorization", "Bearer instance-admin")
	req.Header.Set("X-Aperture-Org", orgID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Entries []storage.AuditEntry `json:"entries"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || len(out.Entries) == 0 {
		t.Errorf("X-Aperture-Org did not name the organization: %d, %d entries", rec.Code, len(out.Entries))
	}

	// An agent attributing itself with X-Aperture-Agent / -Session.
	chat, dlp, _ := chatRouterWithDLP(t)
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"key AKIAIOSFODNN7EXAMPLE"}]}`
	creq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	creq.Header.Set("Authorization", "Bearer ap-test")
	creq.Header.Set("Content-Type", "application/json")
	creq.Header.Set("X-Aperture-Agent", "old-agent")
	creq.Header.Set("X-Aperture-Session", "old-run")
	chat.ServeHTTP(httptest.NewRecorder(), creq)
	events, _ := dlp.List(context.Background(), storage.DefaultOrgID, storage.DLPFilter{})
	if len(events) != 1 || events[0].Agent != "old-agent" || events[0].Session != "old-run" {
		t.Errorf("attribution under the old headers was lost: %+v", events)
	}
}

// A script that creates keys with its own value, under the old field name.
func TestCreatingAKeyAcceptsTheOldFieldName(t *testing.T) {
	h, _ := auditRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")
	rec := owner.do(http.MethodPost, "/admin/keys", `{"name":"ci","aperture_key":"ap-chosen-by-the-script"}`)
	var k storage.Key
	json.Unmarshal(rec.Body.Bytes(), &k)
	if rec.Code != http.StatusCreated || k.MutegateKey != "ap-chosen-by-the-script" {
		t.Errorf("create with aperture_key = %d, key %q", rec.Code, k.MutegateKey)
	}
}
