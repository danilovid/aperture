// Package oauth signs people in with Google, GitHub and Yandex.
//
// It is the authorization-code flow with PKCE, written against the standard
// library: three providers do not justify a dependency, and the parts that
// matter for safety — the state, the verifier, what counts as a verified
// email — are easier to check when they are all in one short file.
//
// The package knows about providers and nothing about accounts. It turns a
// code into a Profile; deciding whose account that is belongs to the server.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Profile is who the provider says signed in.
type Profile struct {
	// ID is the provider's stable id for the person. Email addresses change;
	// this does not, so it is what an identity is keyed by.
	ID string
	// Email is the address the provider has for them, and EmailVerified says
	// whether the provider vouches that it is theirs. Only a verified address
	// may be used to find an existing account: otherwise anybody could sign
	// up with a provider under somebody else's address and walk in.
	Email         string
	EmailVerified bool
	Name          string
}

// Provider is one identity provider and this installation's registration
// with it.
type Provider struct {
	ID   string // "google"
	Name string // "Google"

	ClientID     string
	ClientSecret string

	AuthURL     string
	TokenURL    string
	UserInfoURL string
	// EmailsURL is GitHub's separate list of addresses, the only place it
	// says which are verified.
	EmailsURL string
	Scopes    []string

	profile func(ctx context.Context, p *Provider, client *http.Client, token string) (*Profile, error)
}

// Google, GitHub and Yandex, with their public endpoints.

func Google(clientID, clientSecret string) *Provider {
	return &Provider{
		ID: "google", Name: "Google", ClientID: clientID, ClientSecret: clientSecret,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
		Scopes:      []string{"openid", "email", "profile"},
		profile:     googleProfile,
	}
}

func GitHub(clientID, clientSecret string) *Provider {
	return &Provider{
		ID: "github", Name: "GitHub", ClientID: clientID, ClientSecret: clientSecret,
		AuthURL:     "https://github.com/login/oauth/authorize",
		TokenURL:    "https://github.com/login/oauth/access_token",
		UserInfoURL: "https://api.github.com/user",
		EmailsURL:   "https://api.github.com/user/emails",
		Scopes:      []string{"read:user", "user:email"},
		profile:     githubProfile,
	}
}

func Yandex(clientID, clientSecret string) *Provider {
	return &Provider{
		ID: "yandex", Name: "Yandex", ClientID: clientID, ClientSecret: clientSecret,
		AuthURL:     "https://oauth.yandex.ru/authorize",
		TokenURL:    "https://oauth.yandex.ru/token",
		UserInfoURL: "https://login.yandex.ru/info?format=json",
		Scopes:      []string{"login:email", "login:info"},
		profile:     yandexProfile,
	}
}

// AuthCodeURL is where the browser is sent to sign in.
func (p *Provider) AuthCodeURL(state, challenge, redirectURI string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(p.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if p.ID == "google" {
		// Without this Google skips the account chooser for somebody signed
		// in to one account, which is the wrong account surprisingly often.
		q.Set("prompt", "select_account")
	}
	sep := "?"
	if strings.Contains(p.AuthURL, "?") {
		sep = "&"
	}
	return p.AuthURL + sep + q.Encode()
}

// ErrProvider is anything the provider said no to or answered strangely. The
// detail goes to the log; the person only needs to know it did not work.
var ErrProvider = errors.New("the identity provider did not complete the sign-in")

// maxBody bounds what is read from a provider. Real answers are a few hundred
// bytes; nothing legitimate needs more than this.
const maxBody = 1 << 20

// Exchange trades the code for an access token. The verifier proves this is
// the same browser that started the flow, so a code intercepted on the way
// back is useless to whoever intercepted it.
func (p *Provider) Exchange(ctx context.Context, client *http.Client, code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// GitHub answers form-encoded unless asked otherwise.
	req.Header.Set("Accept", "application/json")

	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	status, err := doJSON(client, req, &out)
	if err != nil {
		return "", fmt.Errorf("%w: token exchange: %v", ErrProvider, err)
	}
	// GitHub reports a bad code as 200 with an "error" field, so the status
	// alone proves nothing.
	if status != http.StatusOK || out.Error != "" || out.AccessToken == "" {
		return "", fmt.Errorf("%w: token exchange: status %d, %s %s", ErrProvider, status, out.Error, out.Description)
	}
	return out.AccessToken, nil
}

// Profile asks the provider who the token belongs to.
func (p *Provider) Profile(ctx context.Context, client *http.Client, token string) (*Profile, error) {
	prof, err := p.profile(ctx, p, client, token)
	if err != nil {
		return nil, fmt.Errorf("%w: profile: %v", ErrProvider, err)
	}
	if prof.ID == "" {
		return nil, fmt.Errorf("%w: profile has no id", ErrProvider)
	}
	return prof, nil
}

func googleProfile(ctx context.Context, p *Provider, client *http.Client, token string) (*Profile, error) {
	var u struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := getJSON(ctx, client, p.UserInfoURL, "Bearer "+token, &u); err != nil {
		return nil, err
	}
	return &Profile{ID: u.Sub, Email: u.Email, EmailVerified: u.EmailVerified, Name: u.Name}, nil
}

func githubProfile(ctx context.Context, p *Provider, client *http.Client, token string) (*Profile, error) {
	var u struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := getJSON(ctx, client, p.UserInfoURL, "Bearer "+token, &u); err != nil {
		return nil, err
	}
	prof := &Profile{ID: strconv.FormatInt(u.ID, 10), Name: u.Name}
	if prof.Name == "" {
		prof.Name = u.Login
	}
	if u.ID == 0 {
		prof.ID = ""
	}

	// The /user address is whatever the person made public, verified or not.
	// The emails list is the only place GitHub says which are verified, so
	// that is where the address comes from.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, client, p.EmailsURL, "Bearer "+token, &emails); err != nil {
		return nil, err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			prof.Email, prof.EmailVerified = e.Email, true
			return prof, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			prof.Email, prof.EmailVerified = e.Email, true
			return prof, nil
		}
	}
	for _, e := range emails {
		if e.Primary {
			prof.Email = e.Email
		}
	}
	return prof, nil
}

func yandexProfile(ctx context.Context, p *Provider, client *http.Client, token string) (*Profile, error) {
	var u struct {
		ID           string `json:"id"`
		Login        string `json:"login"`
		DefaultEmail string `json:"default_email"`
		RealName     string `json:"real_name"`
		DisplayName  string `json:"display_name"`
	}
	// Yandex's info endpoint wants its own scheme name for the same token.
	if err := getJSON(ctx, client, p.UserInfoURL, "OAuth "+token, &u); err != nil {
		return nil, err
	}
	name := u.RealName
	if name == "" {
		name = u.DisplayName
	}
	// Yandex returns no verification flag. It lists only addresses a person
	// has confirmed — a Yandex mailbox is theirs by construction, and an
	// outside address has to be confirmed before Yandex shows it — so a
	// present default_email is treated as verified. That is the trust this
	// flag carries for Yandex, and it is written here rather than implied.
	return &Profile{ID: u.ID, Email: u.DefaultEmail, EmailVerified: u.DefaultEmail != "", Name: name}, nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint, authorization string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	// GitHub refuses requests without one.
	req.Header.Set("User-Agent", "mutegate")
	status, err := doJSON(client, req, out)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("%s answered %d", endpoint, status)
	}
	return nil
}

func doJSON(client *http.Client, req *http.Request, out any) (int, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil && resp.StatusCode == http.StatusOK {
			return resp.StatusCode, fmt.Errorf("unreadable answer: %v", err)
		}
	}
	return resp.StatusCode, nil
}

// NewVerifier returns a PKCE verifier and its S256 challenge.
func NewVerifier() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// HTTPClient is the client providers are called with: bounded, so a provider
// that hangs cannot hold a sign-in open indefinitely.
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}
