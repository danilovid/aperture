package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

// respScanner scans one response as it streams. A stream carries several
// independent text channels — the assistant message, each tool call's
// arguments — and a pattern only makes sense within one of them, so each gets
// its own windowed scanner. Findings from all of them share one verdict: if
// any channel blocks, the stream is torn down.
type respScanner struct {
	h      *Handlers
	meta   reqMeta
	policy inspector.Policy

	channels map[string]*inspector.StreamScanner
	blocked  bool
}

// newRespScanner returns a scanner for the response, or nil when response
// scanning is off for this key — the default, and the zero-overhead path.
func (h *Handlers) newRespScanner(meta reqMeta, p inspector.Policy) *respScanner {
	if h.Inspector == nil || !p.ScanResponses {
		return nil
	}
	return &respScanner{h: h, meta: meta, policy: p, channels: map[string]*inspector.StreamScanner{}}
}

func (rs *respScanner) channel(name string) *inspector.StreamScanner {
	sc := rs.channels[name]
	if sc == nil {
		sc = rs.h.Inspector.NewStreamScanner(rs.policy)
		rs.channels[name] = sc
	}
	return sc
}

// feed pushes streamed text through one channel and returns what may go out.
func (rs *respScanner) feed(channel, text string) (emit string, blocked bool) {
	if rs.blocked {
		return "", true
	}
	emit, verdict := rs.channel(channel).Feed(text)
	if verdict == inspector.ActionBlock {
		rs.blocked = true
		return "", true
	}
	return emit, false
}

// flush drains one channel's window, at the end of its content block.
func (rs *respScanner) flush(channel string) (emit string, blocked bool) {
	if rs.blocked {
		return "", true
	}
	emit, verdict := rs.channel(channel).Flush()
	if verdict == inspector.ActionBlock {
		rs.blocked = true
		return "", true
	}
	return emit, false
}

// redactFinal applies the policy to a terminal event that repeats the whole
// text. Those findings were already recorded from the deltas, so this only
// rewrites — it records nothing.
func (rs *respScanner) redactFinal(text string) string {
	return rs.h.Inspector.RedactText(text, rs.policy)
}

// findings collects every channel's matches once the stream is over.
func (rs *respScanner) findings() (found, suppressed []inspector.Finding) {
	for _, sc := range rs.channels {
		found = append(found, sc.Findings()...)
		suppressed = append(suppressed, sc.Suppressed()...)
	}
	return found, suppressed
}

// record writes the stream's findings to the incident feed. A cancelled
// request context must not drop them, so the caller passes a live one.
func (rs *respScanner) record(ctx context.Context) {
	found, suppressed := rs.findings()
	rs.h.recordFindings(ctx, rs.meta, found, "", storage.DirectionResponse)
	rs.h.recordFindings(ctx, rs.meta, suppressed, "suppressed", storage.DirectionResponse)
}

// blockedRules lists the rules that stopped the stream, for the error frame.
func (rs *respScanner) blockedRules() []string {
	found, _ := rs.findings()
	return blockedRules(found)
}

// ── chat completions ─────────────────────────────────────────────────────────

// chatFilter rewrites OpenAI chunk deltas in flight: content and streamed
// tool-call arguments are scanned, and the held-back tail is released with the
// chunk that carries finish_reason.
func (rs *respScanner) chatFilter() func(line string) (string, bool) {
	return func(line string) (string, bool) {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" {
			return line + "\n", false
		}
		if data == "[DONE]" {
			emit, blocked := rs.flushChat()
			if blocked {
				return rs.chatErrorFrame(), true
			}
			return emit + line + "\n", false
		}

		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			return line + "\n", false
		}

		var pending string // text with no delta of its own to ride on
		finished := false
		for _, c := range choicesOf(chunk) {
			if fr, ok := c["finish_reason"]; ok && fr != nil {
				finished = true
			}
			delta, ok := c["delta"].(map[string]any)
			if !ok {
				continue
			}
			if content, ok := delta["content"].(string); ok {
				emit, blocked := rs.feed("text", content)
				if blocked {
					return rs.chatErrorFrame(), true
				}
				delta["content"] = emit
			}
			for _, call := range toolCallsOf(delta) {
				fn, ok := call["function"].(map[string]any)
				if !ok {
					continue
				}
				args, ok := fn["arguments"].(string)
				if !ok {
					continue
				}
				idx, _ := call["index"].(float64)
				emit, blocked := rs.feed("tool:"+strconv.Itoa(int(idx)), args)
				if blocked {
					return rs.chatErrorFrame(), true
				}
				fn["arguments"] = emit
			}
		}

		// The last chunk of the message releases whatever is still held back.
		if finished {
			rest, blocked := rs.flushChat()
			if blocked {
				return rs.chatErrorFrame(), true
			}
			pending = rest
		}

		b, err := json.Marshal(chunk)
		if err != nil {
			return line + "\n", false
		}
		return pending + "data: " + string(b) + "\n", false
	}
}

// flushChat drains every channel into chunks the client can consume.
func (rs *respScanner) flushChat() (string, bool) {
	var b strings.Builder
	for name := range rs.channels {
		emit, blocked := rs.flush(name)
		if blocked {
			return "", true
		}
		if emit == "" {
			continue
		}
		if name == "text" {
			b.WriteString(chatChunk(map[string]any{"content": emit}))
			continue
		}
		idx, _ := strconv.Atoi(strings.TrimPrefix(name, "tool:"))
		b.WriteString(chatChunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": idx, "function": map[string]any{"arguments": emit},
		}}}))
	}
	return b.String(), false
}

// chatChunk builds a minimal chunk frame carrying a delta of its own.
func chatChunk(delta map[string]any) string {
	b, err := json.Marshal(map[string]any{
		"object":  "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": delta}},
	})
	if err != nil {
		return ""
	}
	return "data: " + string(b) + "\n\n"
}

func choicesOf(chunk map[string]any) []map[string]any {
	raw, _ := chunk["choices"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func toolCallsOf(delta map[string]any) []map[string]any {
	raw, _ := delta["tool_calls"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// chatErrorFrame ends the stream with an OpenAI-shaped error, so the client
// sees why it stopped instead of a truncated answer.
func (rs *respScanner) chatErrorFrame() string {
	rules := rs.blockedRules()
	b, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": "response blocked by DLP policy: sensitive data detected (" +
				strings.Join(rules, ", ") + ")",
			"type":  "aperture_dlp_blocked",
			"rules": rules,
		},
		"aperture": map[string]any{"blocked_by": "dlp", "direction": "response"},
	})
	if err != nil {
		return "data: [DONE]\n\n"
	}
	return "data: " + string(b) + "\n\ndata: [DONE]\n\n"
}

// ── Anthropic Messages ───────────────────────────────────────────────────────

// messagesFilter rewrites Anthropic content-block deltas. Each block index is
// its own channel: text blocks and tool_use JSON never share a window.
func (rs *respScanner) messagesFilter() func(line string) (string, bool) {
	return func(line string) (string, bool) {
		// The tail is released before the block is closed, so it still
		// arrives as part of the block the client is assembling.
		if strings.HasPrefix(line, "event: content_block_stop") {
			emit, blocked := rs.flushMessages()
			if blocked {
				return rs.messagesErrorFrame(), true
			}
			return emit + line + "\n", false
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" {
			return line + "\n", false
		}

		var evt map[string]any
		if json.Unmarshal([]byte(data), &evt) != nil {
			return line + "\n", false
		}
		if t, _ := evt["type"].(string); t != "content_block_delta" {
			return line + "\n", false
		}
		delta, ok := evt["delta"].(map[string]any)
		if !ok {
			return line + "\n", false
		}
		idxF, _ := evt["index"].(float64)
		channel := "block:" + strconv.Itoa(int(idxF))

		field := ""
		switch {
		case delta["text"] != nil:
			field = "text"
		case delta["partial_json"] != nil:
			field = "partial_json"
		default:
			return line + "\n", false
		}
		text, _ := delta[field].(string)
		emit, blocked := rs.feed(channel, text)
		if blocked {
			return rs.messagesErrorFrame(), true
		}
		delta[field] = emit

		b, err := json.Marshal(evt)
		if err != nil {
			return line + "\n", false
		}
		return "data: " + string(b) + "\n", false
	}
}

// flushMessages drains every open block into delta events of its own kind.
func (rs *respScanner) flushMessages() (string, bool) {
	var b strings.Builder
	for name := range rs.channels {
		emit, blocked := rs.flush(name)
		if blocked {
			return "", true
		}
		if emit == "" {
			continue
		}
		idx, _ := strconv.Atoi(strings.TrimPrefix(name, "block:"))
		evt, err := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{"type": "text_delta", "text": emit},
		})
		if err != nil {
			continue
		}
		b.WriteString("event: content_block_delta\ndata: " + string(evt) + "\n\n")
	}
	return b.String(), false
}

func (rs *respScanner) messagesErrorFrame() string {
	rules := rs.blockedRules()
	b, err := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type": "permission_error",
			"message": "response blocked by DLP policy: sensitive data detected (" +
				strings.Join(rules, ", ") + ")",
		},
		"aperture": map[string]any{"blocked_by": "dlp", "direction": "response"},
	})
	if err != nil {
		return "event: error\ndata: {\"type\":\"error\"}\n\n"
	}
	return "event: error\ndata: " + string(b) + "\n\n"
}

// ── Responses API ────────────────────────────────────────────────────────────

// responsesFilter rewrites Responses API deltas. Its terminal events repeat
// the whole text, so those are redacted too — otherwise a client that reads
// response.completed would reassemble what the deltas just hid.
func (rs *respScanner) responsesFilter() func(line string) (string, bool) {
	return func(line string) (string, bool) {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" || data == "[DONE]" {
			return line + "\n", false
		}

		var evt map[string]any
		if json.Unmarshal([]byte(data), &evt) != nil {
			return line + "\n", false
		}
		typ, _ := evt["type"].(string)

		field := ""
		switch typ {
		case "response.output_text.delta", "response.function_call_arguments.delta",
			"response.refusal.delta":
			field = "delta"
		}

		var prefix string
		switch {
		case field != "":
			text, _ := evt[field].(string)
			emit, blocked := rs.feed(responsesChannel(evt), text)
			if blocked {
				return rs.responsesErrorFrame(), true
			}
			evt[field] = emit

		case typ == "response.output_text.done" || typ == "response.function_call_arguments.done":
			emit, blocked := rs.flush(responsesChannel(evt))
			if blocked {
				return rs.responsesErrorFrame(), true
			}
			if emit != "" {
				prefix = responsesDeltaFrame(evt, typ, emit)
			}
			// The done event carries the finished text; redact it to match.
			for _, k := range []string{"text", "arguments"} {
				if full, ok := evt[k].(string); ok {
					evt[k] = rs.redactFinal(full)
				}
			}

		case typ == "response.completed" || typ == "response.incomplete":
			if resp, ok := evt["response"].(map[string]any); ok {
				rs.redactResponseObject(resp)
			}

		default:
			return line + "\n", false
		}

		b, err := json.Marshal(evt)
		if err != nil {
			return line + "\n", false
		}
		return prefix + "data: " + string(b) + "\n", false
	}
}

// responsesChannel keys a scanner per output item and content block, so two
// parallel outputs never share a window.
func responsesChannel(evt map[string]any) string {
	out, _ := evt["output_index"].(float64)
	content, _ := evt["content_index"].(float64)
	return "out:" + strconv.Itoa(int(out)) + ":" + strconv.Itoa(int(content))
}

// responsesDeltaFrame emits the held-back tail as one more delta event before
// the matching done event.
func responsesDeltaFrame(done map[string]any, doneType, text string) string {
	evt := map[string]any{
		"type":          strings.TrimSuffix(doneType, "done") + "delta",
		"delta":         text,
		"output_index":  done["output_index"],
		"content_index": done["content_index"],
		"item_id":       done["item_id"],
	}
	b, err := json.Marshal(evt)
	if err != nil {
		return ""
	}
	return "data: " + string(b) + "\n\n"
}

// redactResponseObject rewrites the finished response object the terminal
// events carry, so no client path reassembles the original text.
func (rs *respScanner) redactResponseObject(resp map[string]any) {
	if text, ok := resp["output_text"].(string); ok {
		resp["output_text"] = rs.redactFinal(text)
	}
	output, _ := resp["output"].([]any)
	for _, it := range output {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if args, ok := item["arguments"].(string); ok {
			item["arguments"] = rs.redactFinal(args)
		}
		blocks, _ := item["content"].([]any)
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				block["text"] = rs.redactFinal(text)
			}
		}
	}
}

func (rs *respScanner) responsesErrorFrame() string {
	rules := rs.blockedRules()
	b, err := json.Marshal(map[string]any{
		"type": "error",
		"code": "aperture_dlp_blocked",
		"message": "response blocked by DLP policy: sensitive data detected (" +
			strings.Join(rules, ", ") + ")",
		"aperture": map[string]any{"blocked_by": "dlp", "direction": "response", "rules": rules},
	})
	if err != nil {
		return "event: error\ndata: {\"type\":\"error\"}\n\n"
	}
	return "event: error\ndata: " + string(b) + "\n\n"
}
