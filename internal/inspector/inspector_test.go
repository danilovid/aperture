package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

func findRules(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func TestScanSecrets(t *testing.T) {
	ins := New()
	p := DefaultPolicy()

	cases := []struct {
		text string
		rule string
	}{
		{"use AKIAIOSFODNN7EXAMPLE for staging", "aws-access-key"},
		{"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 here", "github-token"},
		{"glpat-a1b2c3d4e5f6g7h8i9j0k", "gitlab-token"},
		{"xoxb-123456789012-abcdefABCDEF", "slack-token"},
		{"OPENAI: sk-proj-Abc123XyzDef456Ghi789Jkl", "openai-key"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIE...", "private-key"},
		{"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.SflKxwRJSMeKKF2QT4fwpM", "jwt"},
		{`api_key = "supersecretvalue1234"`, "generic-credential"},
	}
	for _, c := range cases {
		fs := ins.Scan(c.text, p)
		found := false
		for _, f := range fs {
			if f.Rule == c.rule {
				found = true
				if f.Action != ActionBlock {
					t.Errorf("%s: action = %s, want block", c.rule, f.Action)
				}
			}
		}
		if !found {
			t.Errorf("rule %s not found in %q (got %v)", c.rule, c.text, findRules(fs))
		}
	}
}

func TestScanPII(t *testing.T) {
	ins := New()
	p := DefaultPolicy()

	fs := ins.Scan("mail ivan.petrov@corp.io, card 4242 4242 4242 4242, tel +7 916 123-45-67, iban DE89370400440532013000", p)
	want := map[string]bool{"email": false, "credit-card": false, "phone": false, "iban": false}
	for _, f := range fs {
		if _, ok := want[f.Rule]; ok {
			want[f.Rule] = true
			if f.Action != ActionRedact {
				t.Errorf("%s: action = %s, want redact", f.Rule, f.Action)
			}
		}
	}
	for rule, found := range want {
		if !found {
			t.Errorf("rule %s not detected", rule)
		}
	}
}

func TestLuhnRejectsNonCards(t *testing.T) {
	ins := New()
	// 16 digits failing Luhn — e.g. a timestamp-like number.
	fs := ins.Scan("id 1234 5678 9012 3456 ok", DefaultPolicy())
	for _, f := range fs {
		if f.Rule == "credit-card" {
			t.Errorf("luhn-invalid number flagged as credit card")
		}
	}
}

func TestCustomRules(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.CustomRules = []CustomRule{{Name: "project-x", Pattern: `project-x`}}

	fs := ins.Scan("the Project-X launch is friday", p)
	if len(fs) != 1 || fs[0].Rule != "custom:project-x" || fs[0].Action != ActionAlert {
		t.Fatalf("custom rule mismatch: %+v", fs)
	}
}

func TestRedact(t *testing.T) {
	ins := New()
	p := Policy{Secrets: ActionRedact, PII: ActionRedact}
	text := "key AKIAIOSFODNN7EXAMPLE mail a@b.co end"
	fs := ins.Scan(text, p)
	got := Redact(text, fs)
	want := "key [REDACTED:aws-access-key] mail [REDACTED:email] end"
	if got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
}

func TestMaskSampleNeverLeaks(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	masked := MaskSample(secret)
	if strings.Contains(masked, secret[4:]) || masked != "AKIA****************" {
		t.Errorf("MaskSample leaked: %q", masked)
	}
}

func TestScanChatRequestBlockAndRedact(t *testing.T) {
	ins := New()

	// Block: secret in message content.
	body := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}`)
	res := ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionBlock {
		t.Fatalf("verdict = %s, want block", res.Verdict)
	}

	// Redact: PII only.
	body = []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"email ivan@corp.io please"}]}`)
	res = ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %s, want redact", res.Verdict)
	}
	var req struct {
		Messages []struct{ Content string } `json:"messages"`
	}
	if err := json.Unmarshal(res.Body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Messages[0].Content != "email [REDACTED:email] please" {
		t.Errorf("redacted content = %q", req.Messages[0].Content)
	}

	// Clean body passes through unchanged.
	body = []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello world"}]}`)
	res = ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionOff || string(res.Body) != string(body) {
		t.Errorf("clean body altered: verdict=%s", res.Verdict)
	}
}

func TestScanChatRequestMultimodal(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"card 4242424242424242"},{"type":"image_url","image_url":{"url":"data:..."}}]}]}`)
	res := ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %s, want redact", res.Verdict)
	}
	if !strings.Contains(string(res.Body), "[REDACTED:credit-card]") {
		t.Errorf("multimodal text not redacted: %s", res.Body)
	}
}

func BenchmarkScanChatRequest(b *testing.B) {
	ins := New()
	p := DefaultPolicy()
	// Typical request: system prompt + a few messages, ~2KB.
	content := strings.Repeat("Refactor the auth middleware to use context propagation and add tests. ", 10)
	body := []byte(`{"model":"gpt-4o-mini","messages":[` +
		`{"role":"system","content":"You are a helpful coding assistant."},` +
		`{"role":"user","content":"` + content + `"},` +
		`{"role":"assistant","content":"Sure, here is the plan."},` +
		`{"role":"user","content":"` + content + `"}]}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ins.ScanChatRequest(body, p)
	}
}
