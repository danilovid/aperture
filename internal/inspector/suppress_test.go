package inspector

import (
	"strings"
	"testing"
)

// AWS publishes AKIAIOSFODNN7EXAMPLE in its own docs, so it turns up in
// READMEs and tests. Blocking it is the archetypal false positive.
func TestAllowlistSuppressesMatch(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.Allowlist = []string{"AKIAIOSFODNN7EXAMPLE"}

	findings, suppressed := ins.ScanWithSuppressed("docs mention AKIAIOSFODNN7EXAMPLE", p)
	if len(findings) != 0 {
		t.Errorf("allowlisted value still produced a finding: %+v", findings)
	}
	if len(suppressed) != 1 || suppressed[0].Rule != "aws-access-key" {
		t.Fatalf("suppressed not recorded: %+v", suppressed)
	}

	// A different key of the same shape must still be caught.
	findings, _ = ins.ScanWithSuppressed("real key AKIAZZZZZZZZZZZZZZZZ", p)
	if len(findings) != 1 {
		t.Errorf("allowlist leaked to other values: %+v", findings)
	}
}

func TestMutedRuleSuppressesWholeDetector(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.MutedRules = []string{"email"}

	findings, suppressed := ins.ScanWithSuppressed("mail a@b.co and key AKIAIOSFODNN7EXAMPLE", p)
	if len(suppressed) != 1 || suppressed[0].Rule != "email" {
		t.Fatalf("email not suppressed: %+v", suppressed)
	}
	// Other detectors keep working.
	if len(findings) != 1 || findings[0].Rule != "aws-access-key" {
		t.Errorf("muting email affected other rules: %+v", findings)
	}
}

func TestMutedCustomRule(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.CustomRules = []CustomRule{{Name: "project-x", Pattern: "project-x"}}
	p.MutedRules = []string{"custom:project-x"}

	findings, suppressed := ins.ScanWithSuppressed("the project-x launch", p)
	if len(findings) != 0 || len(suppressed) != 1 {
		t.Errorf("custom rule mute failed: findings=%+v suppressed=%+v", findings, suppressed)
	}
}

// A suppressed match must not change the verdict or get redacted.
func TestSuppressedMatchIsNotRedactedOrBlocked(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.Allowlist = []string{"AKIAIOSFODNN7EXAMPLE"}

	body := []byte(`{"messages":[{"role":"user","content":"see AKIAIOSFODNN7EXAMPLE in the docs"}]}`)
	res := ins.ScanChatRequest(body, p)

	if res.Verdict != ActionOff {
		t.Fatalf("verdict = %s, want off", res.Verdict)
	}
	if string(res.Body) != string(body) {
		t.Error("allowlisted body was rewritten; it must pass through byte-identical")
	}
	if len(res.Suppressed) != 1 {
		t.Errorf("suppressed not carried on the result: %+v", res.Suppressed)
	}
	if strings.Contains(string(res.Body), "REDACTED") {
		t.Error("allowlisted value was redacted")
	}
}

func TestAllowlistIsCaseInsensitiveAndPatternBased(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	// a regex, not just a literal: allow any @example.com address
	p.Allowlist = []string{`@example\.com$`}

	findings, suppressed := ins.ScanWithSuppressed("write to Dev@Example.com", p)
	if len(findings) != 0 || len(suppressed) != 1 {
		t.Errorf("pattern allowlist failed: findings=%+v suppressed=%+v", findings, suppressed)
	}
	findings, _ = ins.ScanWithSuppressed("write to dev@corp.io", p)
	if len(findings) != 1 {
		t.Errorf("allowlist over-matched: %+v", findings)
	}
}

func TestInvalidAllowlistPatternIsIgnoredNotFatal(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.Allowlist = []string{"("} // rejected by the admin API, but must not panic here
	findings, _ := ins.ScanWithSuppressed("key AKIAIOSFODNN7EXAMPLE", p)
	if len(findings) != 1 {
		t.Errorf("a broken allowlist entry must not suppress anything: %+v", findings)
	}
}
