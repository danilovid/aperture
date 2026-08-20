package inspector

import "encoding/json"

// ChatResult is the outcome of scanning one chat completions request body.
type ChatResult struct {
	Findings []Finding
	// Suppressed are matches a mute or allowlist entry held back. They carry
	// no verdict but are recorded, so silencing a detector stays visible.
	Suppressed []Finding
	// Verdict is the strictest action across findings (off when clean).
	Verdict Action
	// Body is the request body to send upstream: redacted when the verdict
	// is redact, the original bytes otherwise.
	Body []byte
	// NERError records that the model stage could not be reached. The scan
	// still happened with the regex detectors; the caller logs and counts it.
	NERError error
	// ner caches the model's spans for this body, keyed by text.
	ner map[string][]NERSpan
}

// scanArgs scans a function-call "arguments" field. It holds a JSON-encoded
// string of whatever the model passed to a tool — a common hiding place for
// secrets an agent read from disk. Redaction happens on the decoded string, so
// both the encoded arguments and the outer body stay valid JSON.
func (i *Inspector) scanArgs(fn map[string]any, p Policy, res *ChatResult) bool {
	args, ok := fn["arguments"].(string)
	if !ok {
		return false
	}
	redacted, changed := i.scanString(args, p, res)
	if changed {
		fn["arguments"] = redacted
	}
	return changed
}

// scanToolCalls walks messages[].tool_calls[].function.arguments.
func (i *Inspector) scanToolCalls(calls []any, p Policy, res *ChatResult) bool {
	changed := false
	for _, c := range calls {
		call, ok := c.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := call["function"].(map[string]any)
		if !ok {
			continue
		}
		if i.scanArgs(fn, p, res) {
			changed = true
		}
	}
	return changed
}

// ScanChatRequest scans an OpenAI-format chat completions body: message
// content (string and multimodal block forms), tool-call arguments — including
// the legacy function_call shape — and tool descriptions. Bodies that don't
// parse are passed through untouched; the upstream provider will reject
// malformed JSON.
func (i *Inspector) ScanChatRequest(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return res
	}

	// One model call for the whole body, before the walk.
	i.prescanNER(&res, p, req)

	changed := false

	if messages, ok := req["messages"].([]any); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}

			// content: a string, or multimodal parts. Tool results come back
			// as role:"tool" messages, so they are covered here too.
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

			// Assistant tool calls echoed back in the conversation history.
			if calls, ok := msg["tool_calls"].([]any); ok {
				if i.scanToolCalls(calls, p, &res) {
					changed = true
				}
			}
			// Legacy single-function shape.
			if fc, ok := msg["function_call"].(map[string]any); ok {
				if i.scanArgs(fc, p, &res) {
					changed = true
				}
			}
		}
	}

	// Tool descriptions are user-authored text that ships with every request.
	if tools, ok := req["tools"].([]any); ok {
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
			}
			fn, ok := tool["function"].(map[string]any)
			if !ok {
				continue
			}
			if desc, ok := fn["description"].(string); ok {
				if redacted, ch := i.scanString(desc, p, &res); ch {
					fn["description"] = redacted
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
