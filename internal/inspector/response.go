package inspector

import "encoding/json"

// RedactText applies the policy to a complete string and returns the redacted
// result. It records nothing: it is for terminal streaming events that repeat
// text whose findings were already recorded from the deltas.
func (i *Inspector) RedactText(text string, p Policy) string {
	return Redact(text, i.Scan(text, p))
}

// ScanChatResponse scans a non-streaming chat completions response: the
// assistant message content and any tool-call arguments the model produced.
// A model can echo back a secret it was shown, or put one in a tool call the
// agent then executes elsewhere.
func (i *Inspector) ScanChatResponse(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return res
	}

	// One model call for the whole body, before the walk.
	i.prescanNER(&res, p, resp)

	changed := false
	if choices, ok := resp["choices"].([]any); ok {
		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			msg, ok := choice["message"].(map[string]any)
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
			if calls, ok := msg["tool_calls"].([]any); ok {
				if i.scanToolCalls(calls, p, &res) {
					changed = true
				}
			}
			if fc, ok := msg["function_call"].(map[string]any); ok {
				if i.scanArgs(fc, p, &res) {
					changed = true
				}
			}
		}
	}

	return finishResponse(res, resp, changed)
}

// ScanMessagesResponse scans a non-streaming Anthropic Messages response:
// every content block, including the arguments of tool_use blocks.
func (i *Inspector) ScanMessagesResponse(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return res
	}

	// One model call for the whole body, before the walk.
	i.prescanNER(&res, p, resp)

	changed := false
	if content, ok := resp["content"].([]any); ok {
		if i.scanBlocks(content, p, &res) {
			changed = true
		}
		// tool_use blocks carry an "input" object rather than a text field.
		for _, b := range content {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if input, ok := block["input"].(map[string]any); ok {
				if i.scanMap(input, p, &res) {
					changed = true
				}
			}
		}
	}

	return finishResponse(res, resp, changed)
}

// ScanResponsesResponse scans a non-streaming Responses API result: the output
// items' text blocks and any function-call arguments.
func (i *Inspector) ScanResponsesResponse(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return res
	}

	// One model call for the whole body, before the walk.
	i.prescanNER(&res, p, resp)

	changed := false
	if output, ok := resp["output"].([]any); ok {
		for _, it := range output {
			item, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if content, ok := item["content"].([]any); ok {
				if i.scanBlocks(content, p, &res) {
					changed = true
				}
			}
			// {"type":"function_call","name":…,"arguments":"{…}"}
			if i.scanArgs(item, p, &res) {
				changed = true
			}
		}
	}
	// Convenience field some clients read instead of walking output.
	if text, ok := resp["output_text"].(string); ok {
		if redacted, ch := i.scanString(text, p, &res); ch {
			resp["output_text"] = redacted
			changed = true
		}
	}

	return finishResponse(res, resp, changed)
}

// scanMap walks the string values of a decoded JSON object in place, for the
// free-form argument objects tool calls carry.
func (i *Inspector) scanMap(m map[string]any, p Policy, res *ChatResult) bool {
	changed := false
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if redacted, ch := i.scanString(val, p, res); ch {
				m[k] = redacted
				changed = true
			}
		case map[string]any:
			if i.scanMap(val, p, res) {
				changed = true
			}
		case []any:
			if i.scanBlocks(val, p, res) {
				changed = true
			}
		}
	}
	return changed
}

// finishResponse settles the verdict and re-marshals a rewritten body.
func finishResponse(res ChatResult, resp map[string]any, changed bool) ChatResult {
	res.Verdict = Verdict(res.Findings)
	if res.Verdict == ActionBlock {
		return res // the body never reaches the client
	}
	if changed {
		if b, err := json.Marshal(resp); err == nil {
			res.Body = b
		}
	}
	return res
}
