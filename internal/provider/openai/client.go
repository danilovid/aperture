package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danilovid/aperture/internal/provider"
)

const defaultBaseURL = "https://api.openai.com"

// Client is an OpenAI-compatible API client. It talks to a fixed pair of
// endpoint URLs so it can serve both api.openai.com (which nests routes under
// /v1) and third-party OpenAI-compatible providers whose base URL already
// includes the version segment (DeepSeek, Ollama, Qwen, GLM, …).
type Client struct {
	chatURL    string
	modelsURL  string
	apiKey     string
	httpClient *http.Client
}

// New creates a client for OpenAI itself: routes live under <base>/v1/.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	base := strings.TrimSuffix(baseURL, "/")
	return &Client{
		chatURL:    base + "/v1/chat/completions",
		modelsURL:  base + "/v1/models",
		apiKey:     apiKey,
		httpClient: provider.NewHTTPClient(),
	}
}

// NewCompat creates a client for an OpenAI-compatible provider whose base URL
// already includes any version path (e.g. https://api.deepseek.com/v1,
// http://localhost:11434/v1, https://open.bigmodel.cn/api/paas/v4). Routes are
// <base>/chat/completions and <base>/models.
func NewCompat(baseURL, apiKey string) *Client {
	base := strings.TrimSuffix(baseURL, "/")
	return &Client{
		chatURL:    base + "/chat/completions",
		modelsURL:  base + "/models",
		apiKey:     apiKey,
		httpClient: provider.NewHTTPClient(),
	}
}

// Models returns the list of models.
func (c *Client) Models(ctx context.Context) (io.ReadCloser, string, int, error) {
	return c.doWithStatus(ctx, http.MethodGet, c.modelsURL, nil, "")
}

// ChatCompletions proxies the chat completions request.
func (c *Client) ChatCompletions(ctx context.Context, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	return c.doWithStatus(ctx, http.MethodPost, c.chatURL, body, contentType)
}

func (c *Client) doWithStatus(ctx context.Context, method, url string, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	var reqBody io.Reader = body
	if body != nil && method == http.MethodGet {
		reqBody = nil
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, "", 0, fmt.Errorf("create request: %w", err)
	}

	// Local providers (Ollama, vLLM) are typically unauthenticated.
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")

	if body != nil && method != http.MethodGet {
		// Read body into buffer so we can re-use it (body might be consumed)
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, "", 0, fmt.Errorf("read body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(buf))
		req.ContentLength = int64(len(buf))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("request: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}

	// Caller must close resp.Body
	return resp.Body, ct, resp.StatusCode, nil
}
