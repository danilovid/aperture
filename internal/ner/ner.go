// Package ner talks to a local named-entity recognition service — the stage
// that catches the free-form PII regexes cannot: person names, addresses,
// organisations. The model runs outside the gateway (see ner/ in the repo),
// so Aperture stays a single static binary with no ML runtime linked in.
//
// The contract is one endpoint:
//
//	POST /scan  {"texts":["…","…"]}
//	→ 200      {"results":[{"spans":[{"start":0,"end":9,"label":"PERSON","score":0.97}]}, …]}
//
// A single-text form ({"text":"…"} → {"spans":[…]}) is accepted in replies too,
// so a minimal service is easy to write and easy to poke with curl.
package ner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
)

// Config configures the detector client.
type Config struct {
	// URL is the service base URL, e.g. http://localhost:8081.
	URL string
	// Token, when set, is sent as a bearer token.
	Token string
	// Timeout bounds one call. The whole point of a local model is that this
	// stays small; a slow sidecar must not hold traffic hostage.
	Timeout time.Duration
	// MinScore drops spans the model is unsure about.
	MinScore float64
	// Labels the gateway acts on, upper-case. Empty means the built-in set.
	Labels []string
	// AllowRemote permits a non-local service. Off by default: sending prompt
	// text to a public host is exactly what this product exists to prevent.
	AllowRemote bool
	// FailClosed rejects traffic when the service is unreachable. Off by
	// default — a gateway that dies with its sidecar gets switched off.
	FailClosed bool
	// Observe, when set, receives every call: status is "ok" or "error".
	// Latency is the risk this stage carries, so it is worth a metric.
	Observe func(status string, seconds float64)
}

// defaultLabels are the entity types worth acting on. ORG is left out: company
// names are everywhere in normal prompts and would drown the feed.
var defaultLabels = []string{"PERSON", "PER", "ADDRESS", "LOCATION", "LOC"}

// Client calls the NER service.
type Client struct {
	url      string
	token    string
	http     *http.Client
	minScore float64
	labels   map[string]bool
	observe  func(status string, seconds float64)
}

var _ inspector.NERDetector = (*Client)(nil)

// New validates the configuration and returns a client.
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("NER_URL is empty")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid NER_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid NER_URL scheme %q (want http or https)", u.Scheme)
	}
	if !cfg.AllowRemote {
		local, err := isLocal(u.Hostname())
		if err != nil {
			// A name that does not resolve yet cannot be verified as local —
			// which happens with cluster-internal DNS. Refuse rather than
			// guess, and point at the deliberate override.
			return nil, fmt.Errorf("cannot verify that NER_URL host %q is local (%v); "+
				"set NER_ALLOW_REMOTE=true if you know the service is trusted", u.Hostname(), err)
		}
		if !local {
			return nil, fmt.Errorf("NER_URL %q is not a local or private address; "+
				"prompt text would leave your network. Set NER_ALLOW_REMOTE=true if that is really intended",
				u.Host)
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	labels := map[string]bool{}
	for _, l := range cfg.Labels {
		labels[strings.ToUpper(strings.TrimSpace(l))] = true
	}
	if len(labels) == 0 {
		for _, l := range defaultLabels {
			labels[l] = true
		}
	}
	return &Client{
		url:      strings.TrimSuffix(cfg.URL, "/"),
		token:    cfg.Token,
		http:     &http.Client{Timeout: timeout},
		minScore: cfg.MinScore,
		labels:   labels,
		observe:  cfg.Observe,
	}, nil
}

// scanRequest and scanResponse are the wire shapes.
type scanRequest struct {
	Texts []string `json:"texts"`
}

type spanJSON struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

type scanResponse struct {
	Results []struct {
		Spans []spanJSON `json:"spans"`
	} `json:"results"`
	// Single-text form, so a minimal service can answer one text at a time.
	Spans []spanJSON `json:"spans"`
}

// Detect returns the entity spans for each text, in the same order. Spans the
// gateway does not act on — low score, uninteresting label, offsets outside
// the text — are dropped here rather than downstream.
func (c *Client) Detect(ctx context.Context, texts []string) ([][]inspector.NERSpan, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(scanRequest{Texts: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/scan", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.observed("error", start)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		c.observed("error", start)
		return nil, fmt.Errorf("ner service returned %d", resp.StatusCode)
	}

	var out scanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		c.observed("error", start)
		return nil, fmt.Errorf("decode ner response: %w", err)
	}
	c.observed("ok", start)
	results := make([][]spanJSON, len(texts))
	switch {
	case len(out.Results) > 0:
		for i := range results {
			if i < len(out.Results) {
				results[i] = out.Results[i].Spans
			}
		}
	case len(texts) == 1:
		results[0] = out.Spans
	}

	spans := make([][]inspector.NERSpan, len(texts))
	for i, raw := range results {
		spans[i] = c.keep(raw, texts[i])
	}
	return spans, nil
}

func (c *Client) observed(status string, start time.Time) {
	if c.observe != nil {
		c.observe(status, time.Since(start).Seconds())
	}
}

// keep filters the model's output down to what the gateway will act on, and
// guards the offsets: a buggy service must not make the redactor slice out of
// range or cut a rune in half.
func (c *Client) keep(raw []spanJSON, text string) []inspector.NERSpan {
	var out []inspector.NERSpan
	for _, s := range raw {
		if s.Score < c.minScore {
			continue
		}
		if !c.labels[strings.ToUpper(s.Label)] {
			continue
		}
		if s.Start < 0 || s.End > len(text) || s.Start >= s.End {
			continue
		}
		if !utf8Boundary(text, s.Start) || !utf8Boundary(text, s.End) {
			continue
		}
		out = append(out, inspector.NERSpan{
			Start: s.Start, End: s.End,
			Label: strings.ToUpper(s.Label), Score: s.Score,
		})
	}
	return out
}

// utf8Boundary reports whether i splits text between characters.
func utf8Boundary(text string, i int) bool {
	if i == 0 || i == len(text) {
		return true
	}
	return text[i]&0xC0 != 0x80
}

// isLocal reports whether host is loopback or in a private range — including
// hostnames that resolve there, so a Docker service name works.
func isLocal(host string) (bool, error) {
	if host == "" {
		return false, fmt.Errorf("no host")
	}
	if host == "localhost" {
		return true, nil
	}
	ips := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		ips = append(ips, ip)
	} else {
		resolved, err := net.DefaultResolver.LookupIP(context.Background(), "ip", host)
		if err != nil {
			return false, err
		}
		ips = resolved
	}
	if len(ips) == 0 {
		return false, fmt.Errorf("host resolves to nothing")
	}
	for _, ip := range ips {
		if !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			return false, nil
		}
	}
	return true, nil
}
