package inspector

import "encoding/json"

// scanValue walks any decoded JSON value and scans every string it contains,
// returning the value with redactions applied. The shaped walkers elsewhere in
// this package know one API's field names; this one knows none, which is what
// a decision API needs: its bodies are free-form business fields, and a field
// added upstream tomorrow must not become a blind spot today.
func (i *Inspector) scanValue(v any, p Policy, res *ChatResult) (any, bool) {
	switch t := v.(type) {
	case string:
		redacted, changed := i.scanString(t, p, res)
		return redacted, changed
	case []any:
		changed := false
		for idx, e := range t {
			if out, ch := i.scanValue(e, p, res); ch {
				t[idx] = out
				changed = true
			}
		}
		return t, changed
	case map[string]any:
		changed := false
		for k, e := range t {
			if out, ch := i.scanValue(e, p, res); ch {
				t[k] = out
				changed = true
			}
		}
		return t, changed
	}
	return v, false // numbers, booleans and null carry no text to scan
}

// ScanJevRequest scans a Jev decision request. Unlike the chat APIs there is
// no message list to walk: the agent hands over business fields — the customer
// message, the tool arguments, the policy text — so every string in the body
// is scanned wherever it sits.
func (i *Inspector) ScanJevRequest(body []byte, p Policy) ChatResult {
	return i.scanJevBody(body, p)
}

// ScanJevResponse scans what Jev sends back: the decision envelope carries
// guidance text and per-question answers, which can quote the input.
func (i *Inspector) ScanJevResponse(body []byte, p Policy) ChatResult {
	return i.scanJevBody(body, p)
}

func (i *Inspector) scanJevBody(body []byte, p Policy) ChatResult {
	res := ChatResult{Verdict: ActionOff, Body: body}

	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return res
	}

	// One model call for the whole body, before the walk.
	i.prescanNER(&res, p, doc)

	out, changed := i.scanValue(doc, p, &res)

	res.Verdict = Verdict(res.Findings)
	if res.Verdict == ActionBlock {
		return res // the body never leaves the gateway
	}
	if changed {
		if b, err := json.Marshal(out); err == nil {
			res.Body = b
		}
	}
	return res
}
