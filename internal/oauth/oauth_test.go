package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTripsAndRejectsTampering(t *testing.T) {
	key := DeriveKey("installation secret")
	now := time.Now()
	s := State{Nonce: "n1", Provider: "google", Verifier: "v", Next: "/app/members", Expires: now.Add(time.Minute).Unix()}

	sealed, err := Seal(key, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(key, sealed, "n1", now)
	if err != nil {
		t.Fatalf("a fresh state did not open: %v", err)
	}
	if got.Next != "/app/members" || got.Verifier != "v" {
		t.Errorf("the state came back different: %+v", got)
	}

	cases := map[string]func() (string, string, []byte, time.Time){
		"another nonce": func() (string, string, []byte, time.Time) { return sealed, "n2", key, now },
		"another key": func() (string, string, []byte, time.Time) {
			return sealed, "n1", DeriveKey("a different installation"), now
		},
		"expired": func() (string, string, []byte, time.Time) { return sealed, "n1", key, now.Add(2 * time.Minute) },
		"no signature": func() (string, string, []byte, time.Time) {
			payload, _, _ := strings.Cut(sealed, ".")
			return payload, "n1", key, now
		},
		// Somebody rewriting where to go afterwards, keeping the signature.
		"edited payload": func() (string, string, []byte, time.Time) {
			payload, sig, _ := strings.Cut(sealed, ".")
			body, _ := base64.RawURLEncoding.DecodeString(payload)
			edited := strings.Replace(string(body), "/app/members", "https://evil.example", 1)
			return base64.RawURLEncoding.EncodeToString([]byte(edited)) + "." + sig, "n1", key, now
		},
	}
	for name, c := range cases {
		sealed, nonce, k, at := c()
		if _, err := Open(k, sealed, nonce, at); err != ErrState {
			t.Errorf("%s: opened when it should not have (err = %v)", name, err)
		}
	}
}

func TestVerifierMatchesItsChallenge(t *testing.T) {
	v, c, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(v))
	if c != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Error("the challenge is not the S256 of the verifier")
	}
	v2, _, _ := NewVerifier()
	if v == v2 {
		t.Error("two verifiers came out the same")
	}
}

func TestAuthCodeURLCarriesPKCEAndState(t *testing.T) {
	p := GitHub("client-1", "secret")
	raw := p.AuthCodeURL("the-state", "the-challenge", "https://gw.example/api/auth/oauth/github/callback")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id":             "client-1",
		"state":                 "the-state",
		"code_challenge":        "the-challenge",
		"code_challenge_method": "S256",
		"redirect_uri":          "https://gw.example/api/auth/oauth/github/callback",
		"response_type":         "code",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Get("client_secret") != "" {
		t.Error("the client secret went into the browser's URL")
	}
}

// A provider double: a token endpoint and whatever profile endpoints the
// provider under test uses.
func fakeProvider(t *testing.T, p *Provider, profile map[string]any, emails []map[string]any) *Provider {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") != "the-verifier" {
			// GitHub-style: a failure that is still a 200.
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "tok", "token_type": "bearer"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer tok" && auth != "OAuth tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(profile)
	})
	mux.HandleFunc("GET /emails", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(emails)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.TokenURL = srv.URL + "/token"
	p.UserInfoURL = srv.URL + "/user"
	p.EmailsURL = srv.URL + "/emails"
	return p
}

func signIn(t *testing.T, p *Provider) (*Profile, error) {
	t.Helper()
	ctx := context.Background()
	tok, err := p.Exchange(ctx, http.DefaultClient, "good-code", "the-verifier", "https://gw.example/cb")
	if err != nil {
		return nil, err
	}
	return p.Profile(ctx, http.DefaultClient, tok)
}

func TestExchangeRefusesAWrongVerifier(t *testing.T) {
	p := fakeProvider(t, GitHub("c", "s"), map[string]any{"id": 1}, nil)
	_, err := p.Exchange(context.Background(), http.DefaultClient, "good-code", "a-guess", "https://gw.example/cb")
	if err == nil {
		t.Fatal("a code was exchanged with the wrong verifier")
	}
}

func TestGoogleProfile(t *testing.T) {
	p := fakeProvider(t, Google("c", "s"),
		map[string]any{"sub": "g-123", "email": "a@example.com", "email_verified": true, "name": "Ann"}, nil)
	prof, err := signIn(t, p)
	if err != nil {
		t.Fatal(err)
	}
	if prof.ID != "g-123" || prof.Email != "a@example.com" || !prof.EmailVerified || prof.Name != "Ann" {
		t.Errorf("profile = %+v", prof)
	}
}

func TestGoogleUnverifiedEmailStaysUnverified(t *testing.T) {
	p := fakeProvider(t, Google("c", "s"),
		map[string]any{"sub": "g-1", "email": "someone-else@example.com", "email_verified": false}, nil)
	prof, err := signIn(t, p)
	if err != nil {
		t.Fatal(err)
	}
	if prof.EmailVerified {
		t.Error("an address Google did not verify came back verified")
	}
}

// GitHub's /user address is whatever the person chose to make public. Only
// the emails list says what is verified, so that is the only source trusted.
func TestGitHubTakesTheVerifiedPrimaryAddress(t *testing.T) {
	p := fakeProvider(t, GitHub("c", "s"),
		map[string]any{"id": 42, "login": "octo", "email": "public@example.com"},
		[]map[string]any{
			{"email": "unverified@example.com", "primary": false, "verified": false},
			{"email": "work@example.com", "primary": true, "verified": true},
		})
	prof, err := signIn(t, p)
	if err != nil {
		t.Fatal(err)
	}
	if prof.ID != "42" || prof.Email != "work@example.com" || !prof.EmailVerified {
		t.Errorf("profile = %+v", prof)
	}
	if prof.Name != "octo" {
		t.Errorf("a GitHub account with no name should fall back to its login, got %q", prof.Name)
	}
}

func TestGitHubWithNoVerifiedAddress(t *testing.T) {
	p := fakeProvider(t, GitHub("c", "s"),
		map[string]any{"id": 7, "login": "new"},
		[]map[string]any{{"email": "claimed@example.com", "primary": true, "verified": false}})
	prof, err := signIn(t, p)
	if err != nil {
		t.Fatal(err)
	}
	if prof.EmailVerified {
		t.Error("an unverified GitHub address came back verified")
	}
}

func TestYandexProfile(t *testing.T) {
	p := fakeProvider(t, Yandex("c", "s"),
		map[string]any{"id": "y-9", "login": "ivan", "default_email": "ivan@yandex.ru", "real_name": "Ivan"}, nil)
	prof, err := signIn(t, p)
	if err != nil {
		t.Fatal(err)
	}
	if prof.ID != "y-9" || prof.Email != "ivan@yandex.ru" || !prof.EmailVerified || prof.Name != "Ivan" {
		t.Errorf("profile = %+v", prof)
	}
}
