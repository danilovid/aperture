package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultBaseURL = "https://api.openai.com"

// Client is an OpenAI API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new OpenAI client.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Models returns the list of models from OpenAI.
func (c *Client) Models(ctx context.Context) (io.ReadCloser, string, int, error) {
	return c.doWithStatus(ctx, http.MethodGet, "v1/models", nil, "")
}

// ChatCompletions proxies the chat completions request to OpenAI.
func (c *Client) ChatCompletions(ctx context.Context, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	return c.doWithStatus(ctx, http.MethodPost, "v1/chat/completions", body, contentType)
}

func (c *Client) doWithStatus(ctx context.Context, method, p string, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	url := c.baseURL + "/" + strings.TrimPrefix(p, "/")
	var reqBody io.Reader = body
	if body != nil && method == http.MethodGet {
		reqBody = nil
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, "", 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
