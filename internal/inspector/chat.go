package inspector

import "encoding/json"

// ChatResult is the outcome of scanning one chat completions request body.
type ChatResult struct {
	Findings []Finding
	// Verdict is the strictest action across findings (off when clean).
	Verdict Action
	// Body is the request body to send upstream: redacted when the verdict
	// is redact, the original bytes otherwise.
	Body []byte
}

// ScanChatRequest scans every text part of messages[].content in an
// OpenAI-format chat completions body. Bodies that don't parse are passed
// through untouched — the upstream provider will reject malformed JSON.
func (i *Inspector) ScanChatRequest(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return res
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		return res
	}

	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			findings := i.Scan(content, p)
			if len(findings) == 0 {
				continue
			}
			res.Findings = append(res.Findings, findings...)
			if redacted := Redact(content, findings); redacted != content {
				msg["content"] = redacted
				changed = true
			}
		case []any: // multimodal: [{"type":"text","text":"..."}, ...]
			for _, part := range content {
				pm, ok := part.(map[string]any)
				if !ok {
					continue
				}
				text, ok := pm["text"].(string)
				if !ok {
					continue
				}
				findings := i.Scan(text, p)
				if len(findings) == 0 {
					continue
				}
				res.Findings = append(res.Findings, findings...)
				if redacted := Redact(text, findings); redacted != text {
					pm["text"] = redacted
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
