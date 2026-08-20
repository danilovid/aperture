package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScanChatResponseRedactsAssistantContent(t *testing.T) {
	ins := New()
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"sure: AKIAIOSFODNN7EXAMPLE"}}]}`)
	res := ins.ScanChatResponse(body, Policy{Secrets: ActionRedact})

	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %q, want redact", res.Verdict)
	}
	if strings.Contains(string(res.Body), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret survived in the response body: %s", res.Body)
	}
	if !strings.Contains(string(res.Body), "[REDACTED:aws-access-key]") {
		t.Errorf("not redacted: %s", res.Body)
	}
	if len(res.Findings) != 1 {
		t.Errorf("findings = %+v, want one", res.Findings)
	}
}

// A model can hide a secret in the tool call it asks the agent to run.
func TestScanChatResponseScansToolCallArguments(t *testing.T) {
	ins := New()
	args, _ := json.Marshal(map[string]string{"cmd": "curl -H 'token: ghp_" + strings.Repeat("a", 36) + "'"})
	body, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{map[string]any{
				"function": map[string]any{"name": "shell", "arguments": string(args)},
			}},
		}}},
	})

	res := ins.ScanChatResponse(body, Policy{Secrets: ActionBlock})
	if res.Verdict != ActionBlock {
		t.Fatalf("verdict = %q, want block", res.Verdict)
	}
	if res.Findings[0].Rule != "github-token" {
		t.Errorf("findings = %+v", res.Findings)
	}
}

func TestScanMessagesResponseWalksBlocksAndToolInput(t *testing.T) {
	ins := New()
	body := []byte(`{"content":[
		{"type":"text","text":"here you go: AKIAIOSFODNN7EXAMPLE"},
		{"type":"tool_use","name":"write","input":{"path":"/tmp/x","body":"mail bob@example.com"}}
	]}`)
	res := ins.ScanMessagesResponse(body, Policy{Secrets: ActionRedact, PII: ActionRedact})

	out := string(res.Body)
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(out, "bob@example.com") {
		t.Errorf("sensitive data survived: %s", out)
	}
	if len(res.Findings) != 2 {
		t.Errorf("findings = %+v, want the key and the email", res.Findings)
	}
}

func TestScanResponsesResponseWalksOutput(t *testing.T) {
	ins := New()
	body := []byte(`{"output":[{"type":"message","content":[
		{"type":"output_text","text":"token AKIAIOSFODNN7EXAMPLE"}]}],
		"output_text":"token AKIAIOSFODNN7EXAMPLE"}`)
	res := ins.ScanResponsesResponse(body, Policy{Secrets: ActionRedact})

	out := string(res.Body)
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret survived: %s", out)
	}
	if strings.Count(out, "[REDACTED:aws-access-key]") != 2 {
		t.Errorf("both the block and the convenience field must be redacted: %s", out)
	}
}

// Bodies the gateway does not understand must pass through untouched rather
// than be mangled or dropped.
func TestScanResponseLeavesUnknownShapesAlone(t *testing.T) {
	ins := New()
	for _, body := range [][]byte{
		[]byte(`not json at all`),
		[]byte(`{"choices":"unexpected"}`),
		[]byte(`{"content":{"not":"an array"}}`),
	} {
		for _, scan := range []func([]byte, Policy) ChatResult{
			ins.ScanChatResponse, ins.ScanMessagesResponse, ins.ScanResponsesResponse,
		} {
			res := scan(body, DefaultPolicy())
			if string(res.Body) != string(body) || res.Verdict != ActionOff {
				t.Errorf("body %q was altered: %s (%q)", body, res.Body, res.Verdict)
			}
		}
	}
}

func TestRedactTextRecordsNothing(t *testing.T) {
	ins := New()
	out := ins.RedactText("key AKIAIOSFODNN7EXAMPLE", Policy{Secrets: ActionRedact})
	if out != "key [REDACTED:aws-access-key]" {
		t.Errorf("got %q", out)
	}
}
