package inspector

import (
	"encoding/json"
	"strings"
	"testing"
)

// A Jev body is business fields, not a message list: the secret can sit in a
// nested object, in an array of plain strings, anywhere.
func TestScanJevRequestWalksEveryField(t *testing.T) {
	ins := New()
	body := []byte(`{
		"tool": "issue_customer_refund",
		"action": "Refund after a duplicate charge, key AKIAIOSFODNN7EXAMPLE",
		"arguments_summary": ["order_id=ord_7429", "contact=alice@example.com"],
		"side_effects": ["Moves funds"],
		"policy": ["Refunds above USD 500 require human approval"],
		"reversibility": "partially_reversible"
	}`)

	res := ins.ScanJevRequest(body, Policy{Secrets: ActionRedact, PII: ActionRedact})

	out := string(res.Body)
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret survived in a top-level field: %s", out)
	}
	if strings.Contains(out, "alice@example.com") {
		t.Errorf("email survived inside an array of strings: %s", out)
	}
	if len(res.Findings) != 2 {
		t.Errorf("findings = %+v, want the key and the email", res.Findings)
	}
	// Everything else must survive intact — this body goes on to a decision
	// model, and mangling it changes the answer.
	var doc map[string]any
	if err := json.Unmarshal(res.Body, &doc); err != nil {
		t.Fatalf("redacted body is not valid JSON: %v", err)
	}
	if doc["tool"] != "issue_customer_refund" || doc["reversibility"] != "partially_reversible" {
		t.Errorf("untouched fields changed: %v", doc)
	}
	if args, _ := doc["arguments_summary"].([]any); len(args) != 2 || args[0] != "order_id=ord_7429" {
		t.Errorf("array shape changed: %v", doc["arguments_summary"])
	}
}

// The native endpoint nests the interesting text two levels down.
func TestScanJevRequestWalksNativeStateAndQuestions(t *testing.T) {
	ins := New()
	body := []byte(`{
		"model": "typesafe-ai/jev",
		"state": {
			"customer_message": "I was charged twice, my card is 4242424242424242",
			"duplicate_charge_usd": 680,
			"customer_identity_verified": true
		},
		"questions": {
			"action": {"type": "choice", "instructions": "Choose the safest next action.",
				"criteria": {"allow": "Issue the refund", "deny": "Reject it"}}
		}
	}`)

	res := ins.ScanJevRequest(body, Policy{Secrets: ActionBlock, PII: ActionRedact})

	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %q, want redact", res.Verdict)
	}
	if strings.Contains(string(res.Body), "4242424242424242") {
		t.Errorf("card survived in state: %s", res.Body)
	}
	var doc map[string]any
	json.Unmarshal(res.Body, &doc)
	state, _ := doc["state"].(map[string]any)
	if state["duplicate_charge_usd"] != float64(680) || state["customer_identity_verified"] != true {
		t.Errorf("non-string values were altered: %v", state)
	}
}

func TestScanJevRequestBlocks(t *testing.T) {
	ins := New()
	body := []byte(`{"claim":"the token ghp_` + strings.Repeat("a", 36) + ` is still valid"}`)
	res := ins.ScanJevRequest(body, Policy{Secrets: ActionBlock})
	if res.Verdict != ActionBlock {
		t.Fatalf("verdict = %q, want block", res.Verdict)
	}
}

// Jev's answer quotes the input back in guidance.
func TestScanJevResponseScansGuidance(t *testing.T) {
	ins := New()
	body := []byte(`{"code":0,"message":"ok","data":{"decision":"confirm","confidence":0.86,
		"probabilities":{"allow":0.08,"confirm":0.71},
		"guidance":"Confirm with alice@example.com before refunding","answers":{}}}`)

	res := ins.ScanJevResponse(body, Policy{PII: ActionRedact})
	if strings.Contains(string(res.Body), "alice@example.com") {
		t.Errorf("email survived in the decision envelope: %s", res.Body)
	}
	var doc map[string]any
	json.Unmarshal(res.Body, &doc)
	data, _ := doc["data"].(map[string]any)
	if data["decision"] != "confirm" || data["confidence"] != 0.86 {
		t.Errorf("the decision itself was altered: %v", data)
	}
}

func TestScanJevLeavesUnparseableBodiesAlone(t *testing.T) {
	ins := New()
	for _, body := range [][]byte{[]byte(`not json`), []byte(``), []byte(`[1,2,3`)} {
		res := ins.ScanJevRequest(body, DefaultPolicy())
		if string(res.Body) != string(body) || res.Verdict != ActionOff {
			t.Errorf("body %q was altered: %s", body, res.Body)
		}
	}
}

// Arrays of plain strings used to be skipped inside tool arguments; the same
// walker now covers both.
func TestToolInputArraysAreScanned(t *testing.T) {
	ins := New()
	body := []byte(`{"content":[{"type":"tool_use","name":"write",
		"input":{"tags":["prod","AKIAIOSFODNN7EXAMPLE"]}}]}`)
	res := ins.ScanMessagesResponse(body, Policy{Secrets: ActionRedact})
	if strings.Contains(string(res.Body), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret in an array inside tool input was missed: %s", res.Body)
	}
}
