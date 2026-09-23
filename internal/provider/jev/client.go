// Package jev proxies the Jev decision API (https://www.jevai.org).
//
// Jev is not an LLM endpoint: an agent posts compact business fields — the
// customer message, the tool arguments, the policy text — and gets back a
// typed decision with probabilities. That makes it another way for an agent's
// data to leave the network, which is why Aperture fronts it. Its own docs say
// it plainly: "Do not send passwords, API keys, or unrelated private data."
package jev

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danilovid/aperture/internal/provider"
)

// DefaultBaseURL is the documented host. The apex domain and plain http are
// not supported upstream, so neither are they here.
const DefaultBaseURL = "https://www.jevai.org"

// MaxBodyBytes is the documented request cap. The gateway rejects oversized
// bodies itself rather than spending a round trip to be told.
const MaxBodyBytes = 32 * 1024

// Paths are the documented decision endpoints: five presets and the native
// one. The list is an allowlist — the gateway forwards these and nothing
// else, so it cannot be used as an open proxy to the rest of the host.
var Paths = map[string]string{
	"":            "/api/v1/decisions",
	"tool-guard":  "/api/v1/decisions/tool-guard",
	"model-route": "/api/v1/decisions/model-route",
	"route":       "/api/v1/decisions/route",
	"research":    "/api/v1/decisions/research",
	"completion":  "/api/v1/decisions/completion",
}

// Path returns the upstream path for a preset name, or false when the preset
// is not one Jev documents.
func Path(preset string) (string, bool) {
	p, ok := Paths[strings.Trim(preset, "/")]
	return p, ok
}

// Client calls the Jev decision API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a client. An empty baseURL uses the documented host.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: provider.SharedClient(),
	}
}

// Decide posts a decision request and returns the response untouched. Jev
// answers with one JSON envelope — there is no streaming to relay. The caller
// closes the returned body.
func (c *Client) Decide(ctx context.Context, path string, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, "", 0, fmt.Errorf("create request: %w", err)
	}
	req.ContentLength = int64(len(buf))
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
