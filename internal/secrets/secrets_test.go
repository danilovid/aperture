package secrets

import (
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestEncryptDecryptRoundtrip(t *testing.T) {
	c, err := NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	plain := "sk-proj-verysecretvalue123"
	stored, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(stored) || strings.Contains(stored, plain) {
		t.Fatalf("ciphertext leaks plaintext or missing prefix: %q", stored)
	}
	got, err := c.Decrypt(stored)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Errorf("roundtrip mismatch: %q", got)
	}

	// Two encryptions of the same value differ (random nonce).
	stored2, _ := c.Encrypt(plain)
	if stored == stored2 {
		t.Error("nonce reuse: identical ciphertexts")
	}
}

func TestDecryptPassesThroughPlaintext(t *testing.T) {
	c, _ := NewCipher(testKey)
	got, err := c.Decrypt("sk-legacy-plaintext")
	if err != nil || got != "sk-legacy-plaintext" {
		t.Errorf("plaintext passthrough failed: %q, %v", got, err)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	c1, _ := NewCipher(testKey)
	c2, _ := NewCipher("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	stored, _ := c1.Encrypt("secret")
	if _, err := c2.Decrypt(stored); err == nil {
		t.Error("decrypt with wrong key succeeded")
	}
}

func TestNewCipherValidation(t *testing.T) {
	if _, err := NewCipher("deadbeef"); err == nil {
		t.Error("short key accepted")
	}
	if _, err := NewCipher("not-hex"); err == nil {
		t.Error("non-hex key accepted")
	}
}

func TestHashAndHint(t *testing.T) {
	if HashToken("a") == HashToken("b") {
		t.Error("hash collision on different tokens")
	}
	if len(HashToken("x")) != 64 {
		t.Error("unexpected hash length")
	}
	key := "ap-9f3e1a2b3c4d5e6f7a8bc2a1"
	h := Hint(key)
	if !strings.HasPrefix(h, "ap-9f3e") || !strings.HasSuffix(h, "c2a1") || strings.Contains(h, key[8:len(key)-4]) {
		t.Errorf("hint wrong: %q", h)
	}
	if Hint("short") != "*****" {
		t.Errorf("short hint: %q", Hint("short"))
	}
}
