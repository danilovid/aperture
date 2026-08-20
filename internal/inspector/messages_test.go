package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScanMessagesBlocksSecretInStringContent(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,
		"messages":[{"role":"user","content":"deploy with AKIAIOSFODNN7EXAMPLE"}]}`)

	res := ins.ScanMessagesRequest(body, DefaultPolicy())
	if res.Verdict != ActionBlock {
		t.Fatalf("verdict = %s, want block", res.Verdict)
	}
	if len(res.Findings) != 1 || res.Findings[0].Rule != "aws-access-key" {
		t.Errorf("findings mismatch: %+v", res.Findings)
	}
}

func TestScanMessagesRedactsContentBlocks(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"text","text":"ping ivan@corp.io"},
		{"type":"image","source":{"type":"base64","data":"iVBOR"}}
	]}]}`)

	res := ins.ScanMessagesRequest(body, DefaultPolicy())
	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %s, want redact", res.Verdict)
	}
	if !strings.Contains(string(res.Body), "[REDACTED:email]") {
		t.Errorf("text block not redacted: %s", res.Body)
	}
	if strings.Contains(string(res.Body), "ivan@corp.io") {
		t.Error("original email survived in the upstream body")
	}
	// The non-text block must be preserved untouched.
	if !strings.Contains(string(res.Body), "iVBOR") {
		t.Error("image block was dropped")
	}
}

func TestScanMessagesScansSystemPrompt(t *testing.T) {
	ins := New()
	// system as a plain string
	body := []byte(`{"system":"internal key AKIAIOSFODNN7EXAMPLE","messages":[]}`)
	if res := ins.ScanMessagesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("string system: verdict = %s, want block", res.Verdict)
	}

	// system as content blocks
	body = []byte(`{"system":[{"type":"text","text":"key AKIAIOSFODNN7EXAMPLE"}],"messages":[]}`)
	if res := ins.ScanMessagesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("block system: verdict = %s, want block", res.Verdict)
	}
}

// A tool that read a .env file puts its output in tool_result — a very likely
// place for secrets in agent traffic.
func TestScanMessagesScansToolResult(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"tu_1","content":"AWS_KEY=AKIAIOSFODNN7EXAMPLE"}
	]}]}`)
	if res := ins.ScanMessagesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Fatalf("string tool_result: verdict = %s, want block", res.Verdict)
	}

	// nested block form
	body = []byte(`{"messages":[{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"tu_1","content":[{"type":"text","text":"mail ivan@corp.io"}]}
	]}]}`)
	res := ins.ScanMessagesRequest(body, DefaultPolicy())
	if res.Verdict != ActionRedact {
		t.Fatalf("nested tool_result: verdict = %s, want redact", res.Verdict)
	}
	if !strings.Contains(string(res.Body), "[REDACTED:email]") {
		t.Errorf("nested tool_result not redacted: %s", res.Body)
	}
}

func TestScanMessagesLeavesCleanBodyByteIdentical(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"claude-3-5-haiku-20241022","messages":[{"role":"user","content":"hello"}]}`)
	res := ins.ScanMessagesRequest(body, DefaultPolicy())
	if res.Verdict != ActionOff {
		t.Fatalf("verdict = %s, want off", res.Verdict)
	}
	if string(res.Body) != string(body) {
		t.Error("clean body was rewritten; it must pass through byte-identical")
	}
}

func TestScanMessagesMalformedBodyPassesThrough(t *testing.T) {
	ins := New()
	body := []byte(`{not json`)
	res := ins.ScanMessagesRequest(body, DefaultPolicy())
	if res.Verdict != ActionOff || string(res.Body) != string(body) {
		t.Errorf("malformed body should pass through untouched: %+v", res)
	}
}

func TestScanMessagesRedactedBodyStaysValidJSON(t *testing.T) {
	ins := New()
	body := []byte(`{"system":"contact ivan@corp.io","messages":[{"role":"user","content":"card 4242 4242 4242 4242"}]}`)
	res := ins.ScanMessagesRequest(body, DefaultPolicy())
	var out map[string]any
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("redacted body is not valid JSON: %v", err)
	}
	if s, _ := out["system"].(string); !strings.Contains(s, "[REDACTED:email]") {
		t.Errorf("system not redacted: %v", out["system"])
	}
}
