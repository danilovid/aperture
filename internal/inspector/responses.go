package inspector

import "encoding/json"

// scanResponsesItems walks the Responses API "input" array. Items are either
// messages (content blocks) or function-call records — the latter carry tool
// arguments and tool output, both common hiding places for secrets.
func (i *Inspector) scanResponsesItems(items []any, p Policy, res *ChatResult) bool {
	changed := false
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}

		// {"role":"user","content": "…" | [ {"type":"input_text","text":"…"} ]}
		switch content := item["content"].(type) {
		case string:
			if redacted, ch := i.scanString(content, p, res); ch {
				item["content"] = redacted
				changed = true
			}
		case []any:
			if i.scanBlocks(content, p, res) {
				changed = true
			}
		}

		// {"type":"function_call","name":"…","arguments":"{…}"}
		if i.scanArgs(item, p, res) {
			changed = true
		}

		// {"type":"function_call_output","call_id":"…","output":"…"}
		if out, ok := item["output"].(string); ok {
			if redacted, ch := i.scanString(out, p, res); ch {
				item["output"] = redacted
				changed = true
			}
		}
	}
	return changed
}

// ScanResponsesRequest scans an OpenAI Responses API body: the instructions
// (the system prompt of this API), the input — string or item array, including
// tool arguments and tool output — and tool descriptions. Bodies that don't
// parse are passed through untouched; the upstream provider will reject
// malformed JSON.
func (i *Inspector) ScanResponsesRequest(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return res
	}

	// One model call for the whole body, before the walk.
	i.prescanNER(&res, p, req)

	changed := false

	if instructions, ok := req["instructions"].(string); ok {
		if redacted, ch := i.scanString(instructions, p, &res); ch {
			req["instructions"] = redacted
			changed = true
		}
	}

	switch input := req["input"].(type) {
	case string:
		if redacted, ch := i.scanString(input, p, &res); ch {
			req["input"] = redacted
			changed = true
		}
	case []any:
		if i.scanResponsesItems(input, p, &res) {
			changed = true
		}
	}

	// Responses-API tools are flat: {"type":"function","name":…,"description":…}
	if tools, ok := req["tools"].([]any); ok {
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
			}
			if desc, ok := tool["description"].(string); ok {
				if redacted, ch := i.scanString(desc, p, &res); ch {
					tool["description"] = redacted
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
