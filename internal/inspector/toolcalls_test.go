package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

// The bypass this closes: an agent reads a .env file with a tool, and the
// secret comes back in tool_calls[].function.arguments — not in content.
func TestScanChatBlocksSecretInToolCallArguments(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"gpt-4o-mini","messages":[
		{"role":"user","content":"deploy it"},
		{"role":"assistant","tool_calls":[
			{"id":"call_1","type":"function","function":{
				"name":"write_file",
				"arguments":"{\"path\":\"/etc/app.env\",\"body\":\"AWS_KEY=AKIAIOSFODNN7EXAMPLE\"}"}}]}
	]}`)

	res := ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionBlock {
		t.Fatalf("verdict = %s, want block — secret in tool arguments slipped through", res.Verdict)
	}
	if len(res.Findings) != 1 || res.Findings[0].Rule != "aws-access-key" {
		t.Errorf("findings mismatch: %+v", res.Findings)
	}
}

// Redacting inside a JSON-encoded arguments string must leave both the outer
// body and the arguments payload parseable.
func TestScanChatRedactedArgumentsStayValidJSON(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[
		{"role":"assistant","tool_calls":[
			{"id":"call_1","type":"function","function":{
				"name":"send_mail",
				"arguments":"{\"to\":\"ivan@corp.io\",\"subject\":\"hi\"}"}}]}
	]}`)

	res := ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %s, want redact", res.Verdict)
	}

	// outer body still parses
	var outer struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct{ Arguments string } `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(res.Body, &outer); err != nil {
		t.Fatalf("outer body is not valid JSON: %v", err)
	}

	args := outer.Messages[0].ToolCalls[0].Function.Arguments
	if strings.Contains(args, "ivan@corp.io") {
		t.Error("original address survived in the arguments")
	}
	if !strings.Contains(args, "[REDACTED:email]") {
		t.Errorf("arguments not redacted: %s", args)
	}
	// and the encoded arguments are still parseable JSON themselves
	var inner map[string]any
	if err := json.Unmarshal([]byte(args), &inner); err != nil {
		t.Fatalf("redacted arguments are no longer valid JSON: %v (%s)", err, args)
	}
	if inner["subject"] != "hi" {
		t.Errorf("unrelated argument was damaged: %v", inner)
	}
}

// Tool output arrives as a role:"tool" message; it goes through the normal
// content path. Locked in so a future refactor cannot drop it.
func TestScanChatScansToolRoleContent(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[
		{"role":"tool","tool_call_id":"call_1","content":"AWS_KEY=AKIAIOSFODNN7EXAMPLE"}
	]}`)
	if res := ins.ScanChatRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("verdict = %s, want block", res.Verdict)
	}
}

func TestScanChatScansLegacyFunctionCall(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[
		{"role":"assistant","function_call":{"name":"deploy",
			"arguments":"{\"key\":\"AKIAIOSFODNN7EXAMPLE\"}"}}
	]}`)
	if res := ins.ScanChatRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("verdict = %s, want block", res.Verdict)
	}
}

func TestScanChatScansToolDescriptions(t *testing.T) {
	ins := New()
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{
			"name":"deploy","description":"use key AKIAIOSFODNN7EXAMPLE to authenticate"}}]}`)
	if res := ins.ScanChatRequest(body, DefaultPolicy()); res.Verdict != ActionBlock {
		t.Errorf("verdict = %s, want block", res.Verdict)
	}
}

// A tool-using request with nothing sensitive must pass through byte-identical.
func TestScanChatCleanToolCallUntouched(t *testing.T) {
	ins := New()
	body := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"ls","arguments":"{\"path\":\"/tmp\"}"}}]}]}`)
	res := ins.ScanChatRequest(body, DefaultPolicy())
	if res.Verdict != ActionOff {
		t.Fatalf("verdict = %s, want off", res.Verdict)
	}
	if string(res.Body) != string(body) {
		t.Error("clean body was rewritten; it must pass through byte-identical")
	}
}

// Latency budget: a realistic agent request carries tool calls and tool
// definitions, so the added coverage must stay cheap.
func BenchmarkScanChatWithToolCalls(b *testing.B) {
	ins := New()
	p := DefaultPolicy()
	body := []byte(`{"model":"gpt-4o-mini","messages":[
		{"role":"system","content":"You are a helpful coding assistant with file access."},
		{"role":"user","content":"Refactor the auth middleware and update the tests."},
		{"role":"assistant","tool_calls":[
			{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"internal/server/middleware.go\"}"}},
			{"id":"c2","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"internal/server/middleware_test.go\",\"body\":\"package server\\n\\nimport \\\"testing\\\"\\n\\nfunc TestX(t *testing.T) {}\\n\"}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"package server\n\nfunc loggingMiddleware() {}\n"}
	],
	"tools":[{"type":"function","function":{"name":"read_file","description":"Read a file from the repository"}},
	         {"type":"function","function":{"name":"write_file","description":"Write a file into the repository"}}]}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ins.ScanChatRequest(body, p)
	}
}
