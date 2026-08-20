package inspector

import "encoding/json"

// scanString runs the detectors over s, accumulating findings into res.
// It returns the (possibly redacted) text and whether it changed.
func (i *Inspector) scanString(s string, p Policy, res *ChatResult) (string, bool) {
	findings, suppressed := i.ScanWithSuppressed(s, p)
	res.Suppressed = append(res.Suppressed, suppressed...)
	if len(findings) == 0 {
		return s, false
	}
	res.Findings = append(res.Findings, findings...)
	redacted := Redact(s, findings)
	return redacted, redacted != s
}

// scanBlocks walks an Anthropic content-block array in place: text blocks and
// tool_result payloads (which may nest further blocks). Returns whether any
// block was rewritten.
func (i *Inspector) scanBlocks(blocks []any, p Policy, res *ChatResult) bool {
	changed := false
	for _, b := range blocks {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		// {"type":"text","text":"…"}
		if t, ok := bm["text"].(string); ok {
			if redacted, ch := i.scanString(t, p, res); ch {
				bm["text"] = redacted
				changed = true
			}
		}
		// {"type":"tool_result","content": "…" | [ …blocks… ]}
		switch c := bm["content"].(type) {
		case string:
			if redacted, ch := i.scanString(c, p, res); ch {
				bm["content"] = redacted
				changed = true
			}
		case []any:
			if i.scanBlocks(c, p, res) {
				changed = true
			}
		}
	}
	return changed
}

// ScanMessagesRequest scans a native Anthropic Messages API body: the system
// prompt and every message's content, in both the string and content-block
// forms. Bodies that don't parse are passed through untouched — the upstream
// provider will reject malformed JSON.
func (i *Inspector) ScanMessagesRequest(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return res
	}

	changed := false

	// "system" is a string or an array of content blocks.
	switch sys := req["system"].(type) {
	case string:
		if redacted, ch := i.scanString(sys, p, &res); ch {
			req["system"] = redacted
			changed = true
		}
	case []any:
		if i.scanBlocks(sys, p, &res) {
			changed = true
		}
	}

	if messages, ok := req["messages"].([]any); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			switch content := msg["content"].(type) {
			case string:
				if redacted, ch := i.scanString(content, p, &res); ch {
					msg["content"] = redacted
					changed = true
				}
			case []any:
				if i.scanBlocks(content, p, &res) {
					changed = true
				}
			}
		}
	}

	res.Verdict = Verdict(res.Findings)
	if res.Verdict == ActionBlock {
		return res // upstream is never called; body content is irrelevant
	}
	if changed {
		if b, err := json.Marshal(req); err == nil {
			res.Body = b
		}
	}
	return res
}
