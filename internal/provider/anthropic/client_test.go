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

// upstream answers /v1/messages with body, as text/event-stream when the
// request asked to stream.
func upstream(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "sk-ant-test")
}

func chat(t *testing.T, c *Client, stream bool) string {
	t.Helper()
	body := `{"model":"claude-3-5-haiku","messages":[{"role":"user","content":"hi"}]}`
	if stream {
		body = `{"model":"claude-3-5-haiku","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	}
	rc, _, status, err := c.ChatCompletions(context.Background(), strings.NewReader(body), "application/json")
	if err != nil || status != http.StatusOK {
		t.Fatalf("ChatCompletions: status %d, err %v", status, err)
	}
	defer rc.Close()
	out, _ := io.ReadAll(rc)
	return string(out)
}

// An OpenAI client, and the interceptor that meters spend, read usage off the
// answer. Without it a non-streaming Claude call was logged as free.
func TestNonStreamingAnswerCarriesUsage(t *testing.T) {
	c := upstream(t, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-haiku",
		"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":1200,"output_tokens":300}}`)

	var resp struct {
		Choices []struct {
			Message      struct{ Content string } `json:"message"`
			FinishReason string                   `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(chat(t, c, false)), &resp); err != nil {
		t.Fatal(err)
	}
	if u := resp.Usage; u.PromptTokens != 1200 || u.CompletionTokens != 300 || u.TotalTokens != 1500 {
		t.Errorf("usage = %+v, want 1200/300/1500", u)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "ok" || resp.Choices[0].FinishReason != "stop" {
		t.Errorf("choices = %+v, want one \"ok\" that finished with \"stop\"", resp.Choices)
	}
}

func TestFinishReasonSpeaksOpenAI(t *testing.T) {
	for stop, want := range map[string]string{
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"max_tokens":    "length",
		"refusal":       "content_filter",
		"pause_turn":    "pause_turn", // no counterpart: passed through
	} {
		if got := finishReason(stop); got != want {
			t.Errorf("finishReason(%q) = %q, want %q", stop, got, want)
		}
	}
}

// The stream ends with a usage chunk. message_delta carries the final count,
// and on newer API versions that includes input_tokens as well.
func TestStreamEndsWithTheFinalUsage(t *testing.T) {
	events := func(delta string) string {
		return strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":31,"output_tokens":1}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
			`data: {"type":"message_delta","usage":` + delta + `}`,
			`data: {"type":"message_stop"}`,
		}, "\n\n") + "\n\n"
	}
	for name, tc := range map[string]struct {
		delta   string
		in, out int
	}{
		"output only in the delta": {`{"output_tokens":17}`, 31, 17},
		"input repeated in delta":  {`{"input_tokens":40,"output_tokens":17}`, 40, 17},
	} {
		t.Run(name, func(t *testing.T) {
			out := chat(t, upstream(t, events(tc.delta)), true)
			var usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			}
			for _, line := range strings.Split(out, "\n") {
				var chunk struct {
					Usage *json.RawMessage `json:"usage"`
				}
				data, ok := strings.CutPrefix(line, "data: ")
				if ok && json.Unmarshal([]byte(data), &chunk) == nil && chunk.Usage != nil {
					json.Unmarshal(*chunk.Usage, &usage)
				}
			}
			if usage.PromptTokens != tc.in || usage.CompletionTokens != tc.out {
				t.Errorf("usage = %d/%d, want %d/%d\n%s", usage.PromptTokens, usage.CompletionTokens, tc.in, tc.out, out)
			}
			if !strings.HasSuffix(out, "data: [DONE]\n\n") {
				t.Errorf("stream does not end with [DONE]:\n%s", out)
			}
		})
	}
}
