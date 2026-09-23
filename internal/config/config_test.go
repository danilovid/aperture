package config

import (
	"strings"
	"testing"
	"time"
)

func TestScanResponsesFromEnv(t *testing.T) {
	t.Setenv("DLP_SCAN_RESPONSES", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DLPPolicy.ScanResponses {
		t.Error("DLP_SCAN_RESPONSES=true did not reach the default policy")
	}

	t.Setenv("DLP_SCAN_RESPONSES", "nonsense")
	if _, err := Load(); err == nil {
		t.Error("invalid DLP_SCAN_RESPONSES was accepted")
	}
}

func TestNERConfigFromEnv(t *testing.T) {
	t.Setenv("NER_URL", "http://localhost:8081")
	t.Setenv("NER_TIMEOUT_MS", "80")
	t.Setenv("NER_MIN_SCORE", "0.7")
	t.Setenv("NER_LABELS", "PERSON, ADDRESS")
	t.Setenv("NER_FAIL_CLOSED", "true")
	t.Setenv("DLP_NER", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NER.URL != "http://localhost:8081" || cfg.NER.Timeout != 80*time.Millisecond {
		t.Errorf("ner config = %+v", cfg.NER)
	}
	if cfg.NER.MinScore != 0.7 || len(cfg.NER.Labels) != 2 || !cfg.NER.FailClosed {
		t.Errorf("ner config = %+v", cfg.NER)
	}
	if !cfg.DLPPolicy.NER {
		t.Error("DLP_NER=true did not reach the default policy")
	}

	// Without a URL the stage stays off and the rest is not even parsed.
	t.Setenv("NER_URL", "")
	t.Setenv("NER_MIN_SCORE", "nonsense")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("an unset NER_URL should not fail: %v", err)
	}
	if cfg.NER.URL != "" {
		t.Errorf("ner url = %q, want empty", cfg.NER.URL)
	}

	t.Setenv("NER_URL", "http://localhost:8081")
	if _, err := Load(); err == nil {
		t.Error("invalid NER_MIN_SCORE was accepted")
	}
}

// Open registration is a decision, so it is off until somebody makes it, and a
// value that is neither yes nor no stops the start rather than guessing.
func TestRegistrationIsOffUnlessOpened(t *testing.T) {
	t.Setenv("REGISTRATION_OPEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RegistrationOpen {
		t.Error("registration is open by default")
	}

	t.Setenv("REGISTRATION_OPEN", "true")
	if cfg, err = Load(); err != nil || !cfg.RegistrationOpen {
		t.Errorf("REGISTRATION_OPEN=true: open=%v, err=%v", cfg != nil && cfg.RegistrationOpen, err)
	}

	t.Setenv("REGISTRATION_OPEN", "sometimes")
	if _, err := Load(); err == nil {
		t.Error("REGISTRATION_OPEN=sometimes was accepted")
	}
}

// The project was called Aperture: an installation configured then keeps
// starting, and says which variables to rename.
func TestOldVariableNamesStillWork(t *testing.T) {
	key := strings.Repeat("ab", 32)
	t.Setenv("MUTEGATE_ENCRYPTION_KEY", "")
	t.Setenv("APERTURE_ENCRYPTION_KEY", key)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EncryptionKey != key {
		t.Error("APERTURE_ENCRYPTION_KEY was not read")
	}
	if len(cfg.LegacyEnv) != 1 || cfg.LegacyEnv[0] != "APERTURE_ENCRYPTION_KEY" {
		t.Errorf("legacy variables noted = %v", cfg.LegacyEnv)
	}

	// The new name wins when both are set.
	t.Setenv("MUTEGATE_ENCRYPTION_KEY", strings.Repeat("cd", 32))
	if cfg, _ = Load(); cfg.EncryptionKey != strings.Repeat("cd", 32) || len(cfg.LegacyEnv) != 0 {
		t.Errorf("with both set: key from %v, legacy %v", cfg.EncryptionKey[:4], cfg.LegacyEnv)
	}
}
