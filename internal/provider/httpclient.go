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
//
// A hand-built http.Transport defaults Proxy to nil (unlike http.DefaultTransport),
// so HTTP_PROXY/HTTPS_PROXY/NO_PROXY must be wired explicitly — enterprise
// deployments commonly force all egress through a corporate proxy.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute, // reasoning models can be slow to first byte
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
		},
	}
}
