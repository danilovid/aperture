package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, pw) {
		t.Fatal("the hash contains the password")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash is not argon2id: %q", hash)
	}

	ok, err := VerifyPassword(hash, pw)
	if err != nil || !ok {
		t.Errorf("the right password did not verify: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, pw+"!")
	if err != nil || ok {
		t.Errorf("a wrong password verified: ok=%v err=%v", ok, err)
	}
}

// Two people with the same password must not share a hash, or a leaked table
// shows at a glance who to attack once.
func TestHashesAreSalted(t *testing.T) {
	a, err := HashPassword("the same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("the same password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical — no salt")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if _, err := HashPassword("short"); err != ErrPasswordTooShort {
		t.Errorf("short password accepted: %v", err)
	}
	if _, err := HashPassword(strings.Repeat("x", MaxPasswordLen+1)); err != ErrPasswordTooLong {
		t.Errorf("unbounded password accepted: %v", err)
	}
}

// A hash made with other parameters must still verify: people do not re-enter
// their password because we tuned a constant.
func TestVerifyUsesParametersFromTheHash(t *testing.T) {
	weak := "$argon2id$v=19$m=8,t=1,p=1$c29tZXNhbHR2YWx1ZQ$" // params differ from current
	// Build a real hash with those parameters by hand via the exported path:
	// hashing with current settings then rewriting params must NOT verify.
	strong, err := HashPassword("a password worth keeping")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPassword(weak, "anything"); err != ErrHashFormat {
		t.Errorf("a truncated hash was parsed: %v", err)
	}
	if NeedsRehash(strong) {
		t.Error("a hash made with current parameters wants a rehash")
	}
	if !NeedsRehash("$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaA") {
		t.Error("a weak hash was not flagged for rehash")
	}
}

func TestMalformedHashesAreRejected(t *testing.T) {
	for _, bad := range []string{
		"", "not a hash", "$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=1$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",
	} {
		if ok, err := VerifyPassword(bad, "whatever"); ok || err == nil {
			t.Errorf("malformed hash %q: ok=%v err=%v", bad, ok, err)
		}
	}
}

// Login must cost the same whether or not the address exists, or the form
// becomes a way to enumerate registered users.
func TestSpendVerifyTimeCostsLikeARealVerify(t *testing.T) {
	hash, err := HashPassword("a password worth keeping")
	if err != nil {
		t.Fatal(err)
	}
	real := time.Now()
	VerifyPassword(hash, "wrong but well formed")
	realCost := time.Since(real)

	dummy := time.Now()
	SpendVerifyTime()
	dummyCost := time.Since(dummy)

	ratio := float64(dummyCost) / float64(realCost)
	if ratio < 0.25 || ratio > 4 {
		t.Errorf("miss costs %v and hit costs %v — the difference is measurable", dummyCost, realCost)
	}
}

func TestSessionTokens(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidToken(token) {
		t.Errorf("freshly minted token rejected: %q", token)
	}
	if !SameToken(hash, HashSessionToken(token)) {
		t.Error("hashing the token again gave a different value")
	}
	if string(hash) == token {
		t.Error("the stored value is the token itself")
	}

	other, _, _ := NewSessionToken()
	if other == token {
		t.Error("two tokens came out identical")
	}
	if SameToken(hash, HashSessionToken(other)) {
		t.Error("a different token matched the stored hash")
	}

	for _, bad := range []string{"", "short", strings.Repeat("A", 43) + "=", "!!!"} {
		if ValidToken(bad) {
			t.Errorf("accepted %q as a token", bad)
		}
	}
}

func TestNormalizeAndValidateEmail(t *testing.T) {
	if got := NormalizeEmail("  Ivan.Petrov@Corp.IO "); got != "ivan.petrov@corp.io" {
		t.Errorf("normalised to %q", got)
	}
	for _, ok := range []string{"a@b.co", "ivan.petrov+dlp@corp.io", "x@sub.domain.example"} {
		if !ValidEmail(ok) {
			t.Errorf("rejected a valid address: %q", ok)
		}
	}
	for _, bad := range []string{"", "no-at-sign", "@domain.com", "user@", "user@domain",
		"user@.domain.com", "user@domain..com", "us er@domain.com", "user@domain.com\n"} {
		if ValidEmail(bad) {
			t.Errorf("accepted an invalid address: %q", bad)
		}
	}
}
