package config

import (
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
