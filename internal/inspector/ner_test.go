package inspector

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeDetector stands in for the model service: it marks every occurrence of
// the names it is given.
type fakeDetector struct {
	names []string
	calls int
	texts []string
	err   error
}

func (f *fakeDetector) Detect(_ context.Context, texts []string) ([][]NERSpan, error) {
	f.calls++
	f.texts = append(f.texts, texts...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]NERSpan, len(texts))
	for i, t := range texts {
		for _, name := range f.names {
			if idx := strings.Index(t, name); idx >= 0 {
				out[i] = append(out[i], NERSpan{
					Start: idx, End: idx + len(name), Label: "PERSON", Score: 0.97,
				})
			}
		}
	}
	return out, nil
}

func nerPolicy() Policy {
	return Policy{Secrets: ActionBlock, PII: ActionRedact, Custom: ActionOff, NER: true}
}

func TestNERRedactsNamesRegexCannotCatch(t *testing.T) {
	det := &fakeDetector{names: []string{"Ivan Petrov"}}
	ins := New().WithDetector(det, false)

	res := ins.ScanChatRequest([]byte(`{"model":"gpt-4o","messages":[
		{"role":"user","content":"Ask Ivan Petrov to review the deploy"}]}`), nerPolicy())

	if res.Verdict != ActionRedact {
		t.Fatalf("verdict = %q, want redact", res.Verdict)
	}
	if strings.Contains(string(res.Body), "Ivan Petrov") {
		t.Errorf("name survived: %s", res.Body)
	}
	if !strings.Contains(string(res.Body), "[REDACTED:ner:person]") {
		t.Errorf("not redacted as a person: %s", res.Body)
	}
	if len(res.Findings) != 1 || res.Findings[0].Group != GroupPII {
		t.Errorf("findings = %+v, want one PII finding", res.Findings)
	}
}

// The model must be called once per body, not once per string: latency is the
// whole reason this stage is optional.
func TestNERCallsTheModelOncePerBody(t *testing.T) {
	det := &fakeDetector{names: []string{"Ivan Petrov"}}
	ins := New().WithDetector(det, false)

	ins.ScanChatRequest([]byte(`{"model":"gpt-4o","messages":[
		{"role":"user","content":"first message about Ivan Petrov"},
		{"role":"assistant","content":"second message, also long enough"},
		{"role":"user","content":"third message with Ivan Petrov again"}]}`), nerPolicy())

	if det.calls != 1 {
		t.Errorf("model called %d times, want 1", det.calls)
	}
	if len(det.texts) == 0 {
		t.Fatal("no texts sent to the model")
	}
	for _, sent := range det.texts {
		if len(sent) < 4 {
			t.Errorf("sent a value too short to hold a name: %q", sent)
		}
	}
}

func TestNERIsOffUnlessThePolicyAsksForIt(t *testing.T) {
	det := &fakeDetector{names: []string{"Ivan Petrov"}}
	ins := New().WithDetector(det, false)

	p := nerPolicy()
	p.NER = false
	res := ins.ScanChatRequest([]byte(`{"messages":[{"role":"user","content":"Ask Ivan Petrov about it"}]}`), p)

	if det.calls != 0 {
		t.Errorf("model was called with NER off (%d calls)", det.calls)
	}
	if strings.Contains(string(res.Body), "REDACTED") {
		t.Errorf("body was altered with NER off: %s", res.Body)
	}
}

// With PII off there is nothing a span could be used for, so the call is
// skipped rather than made and thrown away.
func TestNERSkippedWhenPIIIsOff(t *testing.T) {
	det := &fakeDetector{names: []string{"Ivan Petrov"}}
	ins := New().WithDetector(det, false)

	p := nerPolicy()
	p.PII = ActionOff
	ins.ScanChatRequest([]byte(`{"messages":[{"role":"user","content":"Ask Ivan Petrov about it"}]}`), p)

	if det.calls != 0 {
		t.Errorf("model called %d times with pii=off, want 0", det.calls)
	}
}

func TestNERFindingsRespectMuteAndAllowlist(t *testing.T) {
	det := &fakeDetector{names: []string{"Ivan Petrov"}}
	ins := New().WithDetector(det, false)

	p := nerPolicy()
	p.MutedRules = []string{"ner:person"}
	res := ins.ScanChatRequest([]byte(`{"messages":[{"role":"user","content":"Ask Ivan Petrov about it"}]}`), p)
	if len(res.Findings) != 0 || len(res.Suppressed) != 1 {
		t.Errorf("muted rule: findings=%+v suppressed=%+v", res.Findings, res.Suppressed)
	}
	if !strings.Contains(string(res.Body), "Ivan Petrov") {
		t.Errorf("muted match was redacted anyway: %s", res.Body)
	}

	p = nerPolicy()
	p.Allowlist = []string{"Ivan Petrov"}
	res = ins.ScanChatRequest([]byte(`{"messages":[{"role":"user","content":"Ask Ivan Petrov about it"}]}`), p)
	if len(res.Findings) != 0 || len(res.Suppressed) != 1 {
		t.Errorf("allowlisted: findings=%+v suppressed=%+v", res.Findings, res.Suppressed)
	}
}

// An unreachable model degrades to regex-only scanning by default, and the
// caller is told so it can be logged and counted.
func TestNERFailOpenKeepsRegexScanning(t *testing.T) {
	det := &fakeDetector{err: errors.New("connection refused")}
	ins := New().WithDetector(det, false)

	res := ins.ScanChatRequest([]byte(`{"messages":[{"role":"user","content":"key AKIAIOSFODNN7EXAMPLE from Ivan Petrov"}]}`), nerPolicy())

	if res.NERError == nil {
		t.Error("the caller was not told the model stage failed")
	}
	if res.Verdict != ActionBlock {
		t.Errorf("verdict = %q, want block from the regex detector", res.Verdict)
	}
}

func TestNERFailClosedRefusesTraffic(t *testing.T) {
	det := &fakeDetector{err: errors.New("connection refused")}
	ins := New().WithDetector(det, true)

	res := ins.ScanChatRequest([]byte(`{"messages":[{"role":"user","content":"a perfectly clean prompt"}]}`), nerPolicy())

	if res.Verdict != ActionBlock {
		t.Fatalf("verdict = %q, want block when failing closed", res.Verdict)
	}
	if len(res.Findings) != 1 || res.Findings[0].Rule != "ner:unavailable" {
		t.Errorf("findings = %+v, want the reason recorded", res.Findings)
	}
}

// A service that lies about offsets must not corrupt the body or panic.
func TestNERIgnoresImpossibleSpans(t *testing.T) {
	det := &liarDetector{}
	ins := New().WithDetector(det, false)

	body := []byte(`{"messages":[{"role":"user","content":"a perfectly clean prompt"}]}`)
	res := ins.ScanChatRequest(body, nerPolicy())

	if len(res.Findings) != 0 {
		t.Errorf("impossible spans became findings: %+v", res.Findings)
	}
	if string(res.Body) != string(body) {
		t.Errorf("body was altered: %s", res.Body)
	}
}

type liarDetector struct{}

func (liarDetector) Detect(_ context.Context, texts []string) ([][]NERSpan, error) {
	out := make([][]NERSpan, len(texts))
	for i, t := range texts {
		out[i] = []NERSpan{
			{Start: -5, End: 3, Label: "PERSON", Score: 1},
			{Start: 0, End: len(t) + 100, Label: "PERSON", Score: 1},
			{Start: 4, End: 2, Label: "PERSON", Score: 1},
		}
	}
	return out, nil
}

// NER also applies to what the model sends back, when response scanning is on.
func TestNERScansResponses(t *testing.T) {
	det := &fakeDetector{names: []string{"Ivan Petrov"}}
	ins := New().WithDetector(det, false)

	p := nerPolicy()
	p.ScanResponses = true
	res := ins.ScanChatResponse([]byte(`{"choices":[{"message":{"role":"assistant","content":"That would be Ivan Petrov"}}]}`), p)

	if strings.Contains(string(res.Body), "Ivan Petrov") {
		t.Errorf("name survived in the response: %s", res.Body)
	}
}

func TestCollectStringsIsStableAndDeduplicated(t *testing.T) {
	doc := map[string]any{
		"b":    "second value here",
		"a":    "first value here",
		"dup":  "first value here",
		"tiny": "no",
		"deep": []any{map[string]any{"c": "third value here"}},
	}
	first := collectStrings(doc)
	for i := 0; i < 20; i++ {
		if got := collectStrings(doc); len(got) != len(first) || got[0] != first[0] {
			t.Fatalf("order is not stable: %v vs %v", got, first)
		}
	}
	if len(first) != 3 {
		t.Errorf("collected %v, want three distinct values above the minimum length", first)
	}
}
