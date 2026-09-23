package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// State is what a sign-in has to remember between leaving for the provider
// and coming back. It lives in an HttpOnly cookie on the browser that started
// the flow, signed so it can be neither forged nor edited, and it expires
// quickly: a sign-in is a minute's work, not an afternoon's.
type State struct {
	// Nonce is the value sent to the provider as "state" and expected back
	// unchanged. The cookie and the query must agree, which is what stops a
	// stranger's callback being completed in this browser.
	Nonce    string `json:"n"`
	Provider string `json:"p"`
	// Verifier is the PKCE secret. It never leaves this browser and the
	// gateway; the provider only ever sees its hash.
	Verifier string `json:"v"`
	// Next is where to go afterwards, already checked to be a console path.
	Next string `json:"x,omitempty"`
	// Invite is an invitation being redeemed by signing in this way.
	Invite string `json:"i,omitempty"`
	// LinkUser is set when a signed-in person is connecting another way to
	// sign in: the callback links to that account and to no other.
	LinkUser string `json:"l,omitempty"`
	Expires  int64  `json:"e"`
}

// StateLifetime is how long a sign-in may take at the provider.
const StateLifetime = 10 * time.Minute

// ErrState covers every reason a returning sign-in cannot be trusted: no
// cookie, a forged or altered one, an expired one, a mismatched nonce.
var ErrState = errors.New("sign-in state is missing, altered or expired")

// NewNonce returns a random value for State.Nonce.
func NewNonce() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Seal encodes and signs a state for the cookie.
func Seal(key []byte, s State) (string, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + sign(key, payload), nil
}

// Open verifies and decodes a sealed state, and checks it is the one the
// provider sent back and still in date.
func Open(key []byte, sealed, nonce string, now time.Time) (*State, error) {
	payload, sig, ok := strings.Cut(sealed, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(sign(key, payload))) {
		return nil, ErrState
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, ErrState
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, ErrState
	}
	if s.Nonce == "" || !hmac.Equal([]byte(s.Nonce), []byte(nonce)) {
		return nil, ErrState
	}
	if now.Unix() > s.Expires {
		return nil, ErrState
	}
	return &s, nil
}

func sign(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// DeriveKey turns an installation secret into the key states are signed
// with. The label keeps it distinct from anything else derived from the same
// secret, so a signature made here is never valid anywhere else.
func DeriveKey(secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("aperture/oauth-state/v1"))
	return mac.Sum(nil)
}
