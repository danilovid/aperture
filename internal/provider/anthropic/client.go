package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/mutegate/internal/provider"
)

const defaultBaseURL = "https://api.anthropic.com"
const anthropicVersion = "2023-06-01"

// Client is an Anthropic API client with OpenAI-format translation.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new Anthropic client.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: provider.SharedClient(),
	}
}

// Ensure Client implements provider.Provider.
var _ provider.Provider = (*Client)(nil)

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Usage      Usage                   `json:"usage"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// finishReason maps Anthropic's stop_reason onto OpenAI's finish_reason, so a
// client that checks for "stop" or "length" reads the answer the same way
// whichever provider wrote it. A reason with no OpenAI counterpart passes
// through as it is.
func finishReason(stopReason string) string {
	switch stopReason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	default:
		return stopReason
	}
}

// Models lists the models this key can use, from Anthropic's own
// GET /v1/models, in the OpenAI list shape every other provider answers
// with: {"object":"list","data":[{"id","object":"model","created","owned_by"}]}.
// An error from Anthropic comes back as it was, status and body.
func (c *Client) Models(ctx context.Context) (io.ReadCloser, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models?limit=1000", nil)
	if err != nil {
		return nil, "", 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", 0, fmt.Errorf("read models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return io.NopCloser(bytes.NewReader(raw)), resp.Header.Get("Content-Type"), resp.StatusCode, nil
	}

	var list struct {
		Data []struct {
			ID          string    `json:"id"`
			DisplayName string    `json:"display_name"`
			CreatedAt   time.Time `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, "", 0, fmt.Errorf("parse models: %w", err)
	}
	data := make([]map[string]any, 0, len(list.Data))
	for _, m := range list.Data {
		data = append(data, map[string]any{
			"id": m.ID, "object": "model", "created": m.CreatedAt.Unix(),
			"owned_by": "anthropic", "display_name": m.DisplayName,
		})
	}
	b, err := json.Marshal(map[string]any{"object": "list", "data": data})
	if err != nil {
		return nil, "", 0, err
	}
	return io.NopCloser(bytes.NewReader(b)), "application/json", http.StatusOK, nil
}

// ChatCompletions translates OpenAI-format request to Anthropic and back.
func (c *Client) ChatCompletions(ctx context.Context, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read body: %w", err)
	}

	areq, err := toMessages(buf)
	if err != nil {
		var bad badRequest
		if errors.As(err, &bad) {
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": bad.msg, "type": "invalid_request_error"}})
			return io.NopCloser(bytes.NewReader(b)), "application/json", http.StatusBadRequest, nil
		}
		return nil, "", 0, err
	}

	reqBody, _ := json.Marshal(areq)
	url := c.baseURL + "/v1/messages"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("request: %w", err)
	}

	if areq.Stream {
		return c.translateStream(resp)
	}
	return c.translateNonStream(resp)
}

func (c *Client) translateNonStream(resp *http.Response) (io.ReadCloser, string, int, error) {
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, "", 0, err
	}

	if resp.StatusCode >= 400 {
		return io.NopCloser(bytes.NewReader(openAIError(body))), "application/json", resp.StatusCode, nil
	}

	var aresp anthropicResponse
	if err := json.Unmarshal(body, &aresp); err != nil {
		return io.NopCloser(bytes.NewReader(body)), "application/json", resp.StatusCode, nil
	}
	b, _ := json.Marshal(fromMessages(aresp))
	return io.NopCloser(bytes.NewReader(b)), "application/json", resp.StatusCode, nil
}

func (c *Client) translateStream(resp *http.Response) (io.ReadCloser, string, int, error) {
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return io.NopCloser(bytes.NewReader(openAIError(body))), "application/json", resp.StatusCode, nil
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer resp.Body.Close()

		s := &chatStream{w: pw, created: time.Now().Unix(), tools: map[int]int{}}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok && data != "" {
				s.event([]byte(data))
			}
		}
	}()

	return pr, "text/event-stream", resp.StatusCode, nil
}

// chatStream turns Anthropic's stream events into chat completion chunks as
// they arrive: text as content deltas, each tool_use block as a tool call
// whose arguments stream in pieces, the stop reason as finish_reason, and the
// usage in a last chunk before [DONE] — where the interceptor reads it.
type chatStream struct {
	w         io.Writer
	id, model string
	created   int64
	usage     Usage
	// tools maps an Anthropic content block to its OpenAI tool call index.
	tools map[int]int
}

func (s *chatStream) write(v any) {
	b, _ := json.Marshal(v)
	s.w.Write([]byte("data: "))
	s.w.Write(b)
	s.w.Write([]byte("\n\n"))
}

func (s *chatStream) chunk(delta map[string]any, finish any) {
	s.write(map[string]any{
		"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
	})
}

func (s *chatStream) event(data []byte) {
	var evt struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message *struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *Usage `json:"usage"`
		} `json:"message"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *Usage `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &evt) != nil {
		return
	}
	switch evt.Type {
	case "message_start":
		if m := evt.Message; m != nil {
			s.id, s.model = m.ID, m.Model
			if m.Usage != nil {
				s.usage = *m.Usage
			}
		}
		s.chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	case "content_block_start":
		if b := evt.ContentBlock; b != nil && b.Type == "tool_use" {
			n := len(s.tools)
			s.tools[evt.Index] = n
			s.chunk(map[string]any{"tool_calls": []map[string]any{{
				"index": n, "id": b.ID, "type": "function",
				"function": map[string]any{"name": b.Name, "arguments": ""},
			}}}, nil)
		}
	case "content_block_delta":
		d := evt.Delta
		if d == nil {
			return
		}
		switch d.Type {
		case "text_delta":
			if d.Text != "" {
				s.chunk(map[string]any{"content": d.Text}, nil)
			}
		case "input_json_delta":
			if n, ok := s.tools[evt.Index]; ok && d.PartialJSON != "" {
				s.chunk(map[string]any{"tool_calls": []map[string]any{{
					"index": n, "function": map[string]any{"arguments": d.PartialJSON},
				}}}, nil)
			}
		}
	case "message_delta":
		if evt.Usage != nil {
			s.usage.Merge(*evt.Usage)
		}
		if evt.Delta != nil && evt.Delta.StopReason != "" {
			s.chunk(map[string]any{}, finishReason(evt.Delta.StopReason))
		}
	case "message_stop":
		s.write(map[string]any{
			"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model,
			"choices": []any{}, "usage": openAIUsage(s.usage),
		})
		s.w.Write([]byte("data: [DONE]\n\n"))
	case "error":
		if e := evt.Error; e != nil {
			s.write(map[string]any{"error": map[string]any{"message": e.Message, "type": e.Type}})
		}
	}
}

// ── Native Messages API passthrough ───────────────────────────────────────────

// PassthroughHeaders are client headers forwarded verbatim to Anthropic.
// Empty fields fall back to the default API version.
type PassthroughHeaders struct {
	Version string // anthropic-version
	Beta    string // anthropic-beta
}

// Messages proxies a native Anthropic Messages API request without any
// translation: the body goes upstream as-is (after DLP scanning by the caller)
// and the response — including SSE streams — is returned untouched. Caller
// must close the returned ReadCloser.
func (c *Client) Messages(ctx context.Context, body io.Reader, contentType string, hdr PassthroughHeaders) (io.ReadCloser, string, int, error) {
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, "", 0, fmt.Errorf("create request: %w", err)
	}
	req.ContentLength = int64(len(buf))

	req.Header.Set("x-api-key", c.apiKey)
	version := hdr.Version
	if version == "" {
		version = anthropicVersion
	}
	req.Header.Set("anthropic-version", version)
	if hdr.Beta != "" {
		req.Header.Set("anthropic-beta", hdr.Beta)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("request: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	return resp.Body, ct, resp.StatusCode, nil
}

// WithHTTPClient sends this client's requests through hc — a provider's own
// proxy and timeout. It returns the client for chaining.
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	if hc != nil {
		c.httpClient = hc
	}
	return c
}
