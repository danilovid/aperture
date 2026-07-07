package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danilovid/aperture/internal/provider"
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
		httpClient: provider.NewHTTPClient(),
	}
}

// Ensure Client implements provider.Provider.
var _ provider.Provider = (*Client)(nil)

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model      string              `json:"model"`
	MaxTokens  int                 `json:"max_tokens"`
	Messages   []anthropicMessage  `json:"messages"`
	System     string              `json:"system,omitempty"`
	Stream     bool                `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Models returns a minimal models list (Anthropic doesn't expose OpenAI-format /models).
func (c *Client) Models(ctx context.Context) (io.ReadCloser, string, int, error) {
	// Anthropic has no /v1/models endpoint. Return a static list of known Claude models.
	models := map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "claude-sonnet-4-20250514", "object": "model"},
			{"id": "claude-3-5-sonnet-20241022", "object": "model"},
			{"id": "claude-3-5-haiku-20241022", "object": "model"},
			{"id": "claude-3-opus-20240229", "object": "model"},
		},
	}
	b, _ := json.Marshal(models)
	return io.NopCloser(bytes.NewReader(b)), "application/json", 200, nil
}

// ChatCompletions translates OpenAI-format request to Anthropic and back.
func (c *Client) ChatCompletions(ctx context.Context, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read body: %w", err)
	}

	var oai openAIRequest
	if err := json.Unmarshal(buf, &oai); err != nil {
		return nil, "", 0, fmt.Errorf("parse request: %w", err)
	}

	maxTokens := oai.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	var system string
	var messages []anthropicMessage
	for _, m := range oai.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		messages = append(messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	areq := anthropicRequest{
		Model:     oai.Model,
		MaxTokens: maxTokens,
		Messages:  messages,
		Stream:    oai.Stream,
	}
	if system != "" {
		areq.System = system
	}
	if oai.Temperature > 0 {
		areq.Temperature = &oai.Temperature
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

	if oai.Stream {
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
		return io.NopCloser(bytes.NewReader(body)), resp.Header.Get("Content-Type"), resp.StatusCode, nil
	}

	var aresp anthropicResponse
	if err := json.Unmarshal(body, &aresp); err != nil {
		return io.NopCloser(bytes.NewReader(body)), "application/json", resp.StatusCode, nil
	}

	var content string
	for _, block := range aresp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	oaiResp := map[string]any{
		"id":      aresp.ID,
		"object":  "chat.completion",
		"model":   aresp.Model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": aresp.StopReason,
			},
		},
	}
	b, _ := json.Marshal(oaiResp)
	return io.NopCloser(bytes.NewReader(b)), "application/json", resp.StatusCode, nil
}

func (c *Client) translateStream(resp *http.Response) (io.ReadCloser, string, int, error) {
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return io.NopCloser(bytes.NewReader(body)), resp.Header.Get("Content-Type"), resp.StatusCode, nil
	}

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		defer resp.Body.Close()

		var inputTokens, outputTokens int

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" {
				continue
			}

			var evt struct {
				Type    string `json:"type"`
				Message *struct {
					Usage *struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Delta *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}

			switch evt.Type {
			case "message_start":
				if evt.Message != nil && evt.Message.Usage != nil {
					inputTokens = evt.Message.Usage.InputTokens
					outputTokens = evt.Message.Usage.OutputTokens
				}
			case "message_delta":
				if evt.Usage != nil {
					outputTokens = evt.Usage.OutputTokens
				}
			case "content_block_delta":
				if evt.Delta != nil && evt.Delta.Type == "text_delta" && evt.Delta.Text != "" {
					chunk := map[string]any{
						"choices": []map[string]any{
							{"delta": map[string]any{"content": evt.Delta.Text}, "index": 0},
						},
					}
					b, _ := json.Marshal(chunk)
					pw.Write([]byte("data: "))
					pw.Write(b)
					pw.Write([]byte("\n\n"))
				}
			case "message_stop":
				// Emit usage chunk before [DONE] so interceptor can capture tokens.
				usageChunk := map[string]any{
					"usage": map[string]any{
						"prompt_tokens":     inputTokens,
						"completion_tokens": outputTokens,
						"total_tokens":      inputTokens + outputTokens,
					},
					"choices": []any{},
				}
				b, _ := json.Marshal(usageChunk)
				pw.Write([]byte("data: "))
				pw.Write(b)
				pw.Write([]byte("\n\n"))
				pw.Write([]byte("data: [DONE]\n\n"))
			}
		}
	}()

	return pr, "text/event-stream", resp.StatusCode, nil
}
