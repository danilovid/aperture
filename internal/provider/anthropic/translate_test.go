package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An agent's turn, as an OpenAI client sends it: a system prompt and a
// developer one, a picture, two tool calls and their answers, and the tools.
const agentTurn = `{"model":"claude-sonnet-4-5","max_tokens":999,"max_completion_tokens":2000,
 "temperature":0,"stop":"END","user":"u-1","parallel_tool_calls":false,"tool_choice":"required",
 "tools":[
  {"type":"function","function":{"name":"read_file","description":"Read a file",
   "parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}},
  {"type":"function","function":{"name":"now"}}],
 "messages":[
  {"role":"system","content":"You are a coding agent."},
  {"role":"developer","content":[{"type":"text","text":"Be brief."}]},
  {"role":"user","content":[
   {"type":"text","text":"What is in this picture and in main.go?"},
   {"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}},
   {"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]},
  {"role":"assistant","content":null,"tool_calls":[
   {"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}},
   {"id":"call_2","type":"function","function":{"name":"now","arguments":""}}]},
  {"role":"tool","tool_call_id":"call_1","content":"package main"},
  {"role":"tool","tool_call_id":"call_2","content":[{"type":"text","text":"2026-09-24"}]}]}`

func TestTranslationCarriesToolsImagesAndTheirAnswers(t *testing.T) {
	req, err := toMessages([]byte(agentTurn))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(req)
	var got map[string]any
	json.Unmarshal(b, &got)

	want := map[string]any{
		"model":          "claude-sonnet-4-5",
		"max_tokens":     float64(2000), // max_completion_tokens wins over max_tokens
		"temperature":    float64(0),    // zero is a temperature, not an absence
		"system":         "You are a coding agent.\n\nBe brief.",
		"stop_sequences": []any{"END"},
		"metadata":       map[string]any{"user_id": "u-1"},
		"tool_choice":    map[string]any{"type": "any", "disable_parallel_tool_use": true},
		"tools": []any{
			map[string]any{"name": "read_file", "description": "Read a file", "input_schema": map[string]any{
				"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}}},
			map[string]any{"name": "now", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}}},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "What is in this picture and in main.go?"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}},
				map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/a.png"}},
			}},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call_1", "name": "read_file", "input": map[string]any{"path": "main.go"}},
				map[string]any{"type": "tool_use", "id": "call_2", "name": "now", "input": map[string]any{}},
			}},
			// Two tool answers in a row are one user turn: roles must alternate.
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "package main"},
				map[string]any{"type": "tool_result", "tool_use_id": "call_2", "content": "2026-09-24"},
			}},
		},
	}
	for k, w := range want {
		wb, _ := json.Marshal(w)
		gb, _ := json.Marshal(got[k])
		if string(wb) != string(gb) {
			t.Errorf("%s:\n got %s\nwant %s", k, gb, wb)
		}
	}
}

func TestToolChoice(t *testing.T) {
	for in, want := range map[string]string{
		`"auto"`: `{"type":"auto"}`,
		`"none"`: `{"type":"none"}`,
		`{"type":"function","function":{"name":"read_file"}}`: `{"name":"read_file","type":"tool"}`,
	} {
		got, err := toolChoice(json.RawMessage(in))
		b, _ := json.Marshal(got)
		if err != nil || string(b) != want {
			t.Errorf("toolChoice(%s) = %s, %v; want %s", in, b, err, want)
		}
	}
}

// What cannot be carried is refused as a 400, never quietly dropped: a model
// that answers without the file it was shown is worse than an error.
func TestTranslationRefusesWhatItCannotCarry(t *testing.T) {
	for name, body := range map[string]string{
		"audio":             `{"model":"c","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"x","format":"wav"}}]}]}`,
		"arguments":         `{"model":"c","messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"f","arguments":"{not json"}}]}]}`,
		"role":              `{"model":"c","messages":[{"role":"function","name":"f","content":"x"}]}`,
		"image url":         `{"model":"c","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"file:///etc/passwd"}}]}]}`,
		"tool choice":       `{"model":"c","tool_choice":"sometimes","messages":[]}`,
		"image in a system": `{"model":"c","messages":[{"role":"system","content":[{"type":"image_url","image_url":{"url":"https://x/y.png"}}]}]}`,
	} {
		c := upstream(t, `{}`)
		rc, _, status, err := c.ChatCompletions(context.Background(), strings.NewReader(body), "application/json")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		b, _ := io.ReadAll(rc)
		var e struct {
			Error struct{ Message, Type string } `json:"error"`
		}
		json.Unmarshal(b, &e)
		if status != http.StatusBadRequest || e.Error.Type != "invalid_request_error" || e.Error.Message == "" {
			t.Errorf("%s: %d %s", name, status, b)
		}
	}
}

func TestNonStreamingToolCallComesBackAsOne(t *testing.T) {
	c := upstream(t, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5",
		"content":[{"type":"text","text":"Let me look."},
		           {"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"main.go"}}],
		"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
	var resp struct {
		Choices []struct {
			Message struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct{ Name, Arguments string }
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(chat(t, c, false)), &resp); err != nil {
		t.Fatal(err)
	}
	ch := resp.Choices[0]
	if ch.Message.Content == nil || *ch.Message.Content != "Let me look." || ch.FinishReason != "tool_calls" {
		t.Errorf("message = %+v, finish = %q", ch.Message, ch.FinishReason)
	}
	if len(ch.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", ch.Message.ToolCalls)
	}
	tc := ch.Message.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Type != "function" || tc.Function.Name != "read_file" || tc.Function.Arguments != `{"path":"main.go"}` {
		t.Errorf("tool call = %+v", tc)
	}
}

// Streamed, the tool call arrives as OpenAI streams one: named in its first
// chunk, its arguments in pieces under the same index.
func TestStreamedToolCallArrivesInPieces(t *testing.T) {
	events := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me look."}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"ping"}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pa"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"th\":\"main.go\"}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	out := chat(t, upstream(t, events), true)
	if !strings.HasSuffix(out, "data: [DONE]\n\n") {
		t.Fatalf("stream does not end with [DONE]:\n%s", out)
	}

	var content, role, finish string
	type call struct{ id, name, args string }
	calls := map[int]*call{}
	for _, line := range strings.Split(out, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var ch struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Choices []struct {
				Delta struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct{ Name, Arguments string }
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			t.Fatalf("bad chunk %s", data)
		}
		if ch.ID != "msg_1" || ch.Object != "chat.completion.chunk" {
			t.Errorf("chunk without id or object: %s", data)
		}
		for _, c := range ch.Choices {
			if c.Delta.Role != "" {
				role = c.Delta.Role
			}
			content += c.Delta.Content
			for _, tc := range c.Delta.ToolCalls {
				if calls[tc.Index] == nil {
					calls[tc.Index] = &call{}
				}
				if tc.ID != "" {
					calls[tc.Index].id = tc.ID
				}
				if tc.Function.Name != "" {
					calls[tc.Index].name = tc.Function.Name
				}
				calls[tc.Index].args += tc.Function.Arguments
			}
			if c.FinishReason != nil {
				finish = *c.FinishReason
			}
		}
	}
	if role != "assistant" || content != "Let me look." || finish != "tool_calls" {
		t.Errorf("role %q, content %q, finish %q", role, content, finish)
	}
	if c := calls[0]; c == nil || c.id != "toolu_1" || c.name != "read_file" || c.args != `{"path":"main.go"}` || len(calls) != 1 {
		t.Errorf("tool calls = %+v", calls)
	}
}

// An error from Anthropic reaches an OpenAI client in the shape it parses.
func TestUpstreamErrorSpeaksOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: too large"}}`)
	}))
	defer srv.Close()
	for _, stream := range []bool{false, true} {
		body := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`
		if stream {
			body = `{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		}
		rc, _, status, err := New(srv.URL, "k").ChatCompletions(context.Background(), strings.NewReader(body), "application/json")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		if status != http.StatusBadRequest || string(b) != `{"error":{"message":"max_tokens: too large","type":"invalid_request_error"}}` {
			t.Errorf("stream=%v: %d %s", stream, status, b)
		}
	}
}
