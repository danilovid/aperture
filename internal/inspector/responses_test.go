package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScanResponsesStringInput(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"gpt-4o-mini","input":"deploy with AKIAIOSFODNN7EXAMPLE"}`)
	if res := ins.ScanResponsesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Fatalf("verdict = %s, want block", res.Verdict)
	}
}

func TestScanResponsesInstructions(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"gpt-4o-mini","instructions":"auth with AKIAIOSFODNN7EXAMPLE","input":"hi"}`)
	if res := ins.ScanResponsesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("instructions not scanned: verdict = %s", res.Verdict)
	}
}

func TestScanResponsesContentBlocks(t *testing.T) {
	ins := New()
	body := []byte(`{"input":[{"role":"user","content":[
		{"type":"input_text","text":"reach ivan@corp.io"},
		{"type":"input_image","image_url":"data:image/png;base64,iVBOR"}]}]}`)

	res := ins.ScanResponsesRequest(body, DefaultPolicy())
	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %s, want redact", res.Verdict)
	}
	if !strings.Contains(string(res.Body), "[REDACTED:email]") {
		t.Errorf("text block not redacted: %s", res.Body)
	}
	if !strings.Contains(string(res.Body), "iVBOR") {
		t.Error("image block was dropped")
	}
}

// The Responses equivalent of the tool_calls bypass.
func TestScanResponsesFunctionCallArguments(t *testing.T) {
	ins := New()
	body := []byte(`{"input":[
		{"type":"function_call","call_id":"c1","name":"write_file",
		 "arguments":"{\"body\":\"AWS_KEY=AKIAIOSFODNN7EXAMPLE\"}"}]}`)
	if res := ins.ScanResponsesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Fatalf("function_call arguments not scanned: verdict = %s", res.Verdict)
	}
}

// Tool output — where a file the agent just read comes back.
func TestScanResponsesFunctionCallOutput(t *testing.T) {
	ins := New()
	body := []byte(`{"input":[
		{"type":"function_call_output","call_id":"c1","output":"AWS_KEY=AKIAIOSFODNN7EXAMPLE"}]}`)
	if res := ins.ScanResponsesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Fatalf("function_call_output not scanned: verdict = %s", res.Verdict)
	}

	// redaction keeps the field a plain string
	body = []byte(`{"input":[{"type":"function_call_output","call_id":"c1","output":"mail ivan@corp.io"}]}`)
	res := ins.ScanResponsesRequest(body, DefaultPolicy())
	var out struct {
		Input []struct{ Output string } `json:"input"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("redacted body is not valid JSON: %v", err)
	}
	if !strings.Contains(out.Input[0].Output, "[REDACTED:email]") {
		t.Errorf("output not redacted: %q", out.Input[0].Output)
	}
}

func TestScanResponsesToolDescriptions(t *testing.T) {
	ins := New()
	body := []byte(`{"input":"hi","tools":[
		{"type":"function","name":"deploy","description":"use AKIAIOSFODNN7EXAMPLE"}]}`)
	if res := ins.ScanResponsesRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("tool description not scanned: verdict = %s", res.Verdict)
	}
}

func TestScanResponsesCleanBodyUntouched(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"gpt-4o-mini","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	res := ins.ScanResponsesRequest(body, DefaultPolicy())
	if res.Verdict != ActionOff || string(res.Body) != string(body) {
		t.Errorf("clean body altered: verdict=%s", res.Verdict)
	}
}

func TestScanResponsesRespectsAllowlist(t *testing.T) {
	ins := New()
	p := DefaultPolicy()
	p.Allowlist = []string{"AKIAIOSFODNN7EXAMPLE"}
	body := []byte(`{"input":"docs show AKIAIOSFODNN7EXAMPLE"}`)
	res := ins.ScanResponsesRequest(body, p)
	if res.Verdict != ActionOff || len(res.Suppressed) != 1 {
		t.Errorf("allowlist not applied on the responses path: %+v", res)
	}
}
