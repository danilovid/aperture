package inspector

import (
	"context"
	"sort"
)

// NERSpan is one entity the external model found in a text, in byte offsets.
type NERSpan struct {
	Start int
	End   int
	Label string
	Score float64
}

// NERDetector is the model stage: it turns free-form text into entity spans.
// It lives outside the gateway (see internal/ner), so no ML runtime is linked
// into the binary and nothing has to leave the machine.
type NERDetector interface {
	Detect(ctx context.Context, texts []string) ([][]NERSpan, error)
}

// WithDetector returns a copy of the inspector that also runs the model stage
// for policies with NER enabled. failClosed decides what an unreachable model
// means: refuse the traffic, or scan it with regexes alone.
func (i *Inspector) WithDetector(d NERDetector, failClosed bool) *Inspector {
	c := *i
	c.detector = d
	c.nerFailClosed = failClosed
	return &c
}

// WithContext returns a request-scoped copy, so the model call is bound to the
// request's deadline and cancellation — the same pattern as
// http.Request.WithContext.
func (i *Inspector) WithContext(ctx context.Context) *Inspector {
	c := *i
	c.ctx = ctx
	return &c
}

func (i *Inspector) context() context.Context {
	if i.ctx != nil {
		return i.ctx
	}
	return context.Background()
}

// nerRuleName maps a model label to the rule name in the incident feed.
// Kept here so a finding reads the same whichever service produced it.
func nerRuleName(label string) string {
	switch label {
	case "PERSON", "PER":
		return "ner:person"
	case "ADDRESS":
		return "ner:address"
	case "LOCATION", "LOC":
		return "ner:location"
	case "ORG", "ORGANIZATION":
		return "ner:org"
	}
	return "ner:" + lower(label)
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// ScanText scans one plain string, model stage included. The dry-run endpoint
// uses it, so what the console previews is what live traffic would get.
func (i *Inspector) ScanText(text string, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff}
	i.prescanNER(&res, p, text)
	redacted, _ := i.scanString(text, p, &res)
	res.Verdict = Verdict(res.Findings)
	res.Body = []byte(redacted)
	return res
}

// prescanNER runs the model once for the whole body and caches the spans by
// text. The walkers then look them up instead of making a call per string:
// one request must cost one model call, not twenty.
func (i *Inspector) prescanNER(res *ChatResult, p Policy, doc any) {
	if !p.NER || i.detector == nil {
		return
	}
	// NER findings are PII and follow the pii action, so with PII off there is
	// nothing the result could be used for — skip the call entirely.
	if p.ActionFor(GroupPII) == ActionOff {
		return
	}
	texts := collectStrings(doc)
	if len(texts) == 0 {
		return
	}
	spans, err := i.detector.Detect(i.context(), texts)
	if err != nil {
		res.NERError = err
		if i.nerFailClosed {
			// Fail closed: the request is refused, and the reason lands in the
			// feed instead of silently degrading to regex-only scanning.
			res.Findings = append(res.Findings, Finding{
				Rule:         "ner:unavailable",
				Group:        GroupPII,
				Action:       ActionBlock,
				MaskedSample: "model unreachable",
			})
		}
		return
	}
	res.ner = make(map[string][]NERSpan, len(texts))
	for idx, t := range texts {
		if idx < len(spans) && len(spans[idx]) > 0 {
			res.ner[t] = spans[idx]
		}
	}
}

// nerFindings turns the cached spans for one string into findings, applying
// the same mute and allowlist rules the regex detectors get.
func (i *Inspector) nerFindings(s string, p Policy, res *ChatResult) (found, suppressed []Finding) {
	spans := res.ner[s]
	if len(spans) == 0 {
		return nil, nil
	}
	action := p.ActionFor(GroupPII)
	for _, span := range spans {
		if span.Start < 0 || span.End > len(s) || span.Start >= span.End {
			continue // a service that lies about offsets must not corrupt text
		}
		match := s[span.Start:span.End]
		f := Finding{
			Rule:         nerRuleName(span.Label),
			Group:        GroupPII,
			Action:       action,
			Start:        span.Start,
			End:          span.End,
			MaskedSample: MaskSample(match),
		}
		if p.isMuted(f.Rule) || p.allows(match) {
			suppressed = append(suppressed, f)
			continue
		}
		found = append(found, f)
	}
	sort.Slice(found, func(a, b int) bool { return found[a].Start < found[b].Start })
	return found, suppressed
}

// collectStrings gathers every string in a decoded JSON document, de-duplicated
// and in a stable order. Short values cannot hold a name or an address, so they
// are skipped rather than sent to the model.
func collectStrings(doc any) []string {
	const minLen = 4
	var out []string
	seen := map[string]bool{}

	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			if len(t) >= minLen && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys) // map order is random; the batch must not be
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(doc)
	return out
}
