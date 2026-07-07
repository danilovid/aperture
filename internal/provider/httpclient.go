package provider

import (
	"net"
	"net/http"
	"time"
)

// NewHTTPClient returns an http.Client for upstream LLM calls.
// There is deliberately no overall Timeout: streaming responses can run for
// minutes. Instead, connection setup and time-to-first-byte are bounded, and
// request contexts cancel abandoned calls.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute, // reasoning models can be slow to first byte
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
		},
	}
}
