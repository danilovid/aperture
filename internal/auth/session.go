package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Session lifetime. Thirty days is long enough that people are not logged out
// mid-week, short enough that a stolen laptop stops being useful.
const (
	SessionLifetime = 30 * 24 * time.Hour
	// SessionRenewAfter is how much of the lifetime must pass before an
	// active session's expiry is pushed out. Renewing on every request would
	// mean a database write per page view.
	SessionRenewAfter = 24 * time.Hour
	// SessionCookie is the cookie the console carries.
	SessionCookie = "mutegate_session"
	// CSRFHeader must be present on every mutating request. Same-origin SPA
	// plus SameSite=Lax makes a separate token unnecessary; a header a form
	// post cannot set is what actually stops cross-site writes.
	CSRFHeader = "X-Mutegate-CSRF"
	// tokenBytes is the entropy in a session token.
	tokenBytes = 32
)

// ErrBadToken means the presented value is not shaped like a session token.
// It is deliberately indistinguishable from "no such session" to the caller.
var ErrBadToken = errors.New("invalid session token")

// NewSessionToken returns the token to hand to the browser and the hash to
// store. The raw token never touches the database: a dump of the sessions
// table must not let anyone log in as somebody else.
func NewSessionToken() (token string, hash []byte, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashSessionToken(token), nil
}

// HashSessionToken hashes a presented token for lookup. Session tokens are
// high-entropy random values, not passwords, so a fast hash is the right tool
// — there is nothing to brute force.
func HashSessionToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ValidToken reports whether a presented value could be a token at all,
// cheaply, before any database work.
func ValidToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil
}

// SameToken compares two token hashes in constant time.
func SameToken(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

// NormalizeEmail lower-cases and trims an address so that one person cannot
// register twice by changing capitalisation. The local part is technically
// case-sensitive in the RFC and in practice never is.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidEmail is a deliberately loose check: the only real proof an address
// works is mail sent to it, and rejecting unusual-but-valid addresses annoys
// exactly the people who already have trouble signing up everywhere else.
func ValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	local, domain := email[:at], email[at+1:]
	if strings.ContainsAny(email, " \t\r\n") || strings.Contains(domain, "..") {
		return false
	}
	return local != "" && strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") &&
		!strings.HasSuffix(domain, ".")
}
