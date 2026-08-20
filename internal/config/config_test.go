package config

import "testing"

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
