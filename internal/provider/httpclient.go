package provider

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	return &http.Client{Transport: newTransport(http.ProxyFromEnvironment, DefaultHeaderTimeout)}
}

// DefaultHeaderTimeout is how long an upstream may take to send its first
// byte. Reasoning models can be slow to start, so it is generous.
const DefaultHeaderTimeout = 2 * time.Minute

func newTransport(proxy func(*http.Request) (*url.URL, error), headerTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   16,
	}
}

var (
	sharedOnce   sync.Once
	sharedClient *http.Client
)

// SharedClient is the process-wide client for upstreams with nothing of
// their own configured. One client, one connection pool: a client built per
// request would throw its pool away and pay a TLS handshake every time.
func SharedClient() *http.Client {
	sharedOnce.Do(func() { sharedClient = NewHTTPClient() })
	return sharedClient
}

// ParseProxyURL checks a proxy address someone typed. net/http speaks HTTP,
// HTTPS and SOCKS5 proxies; anything else would fail on the first request,
// so it fails here instead.
func ParseProxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("not a proxy address: want something like http://proxy.internal:3128")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	}
	return nil, fmt.Errorf("unsupported proxy scheme %q: want http, https or socks5", u.Scheme)
}

// Transports hands out clients by proxy and timeout, building each transport
// once. Providers that share a proxy share a connection pool, and nothing is
// rebuilt per request.
type Transports struct {
	mu      sync.Mutex
	clients map[string]*http.Client
}

// NewTransports returns an empty cache.
func NewTransports() *Transports {
	return &Transports{clients: map[string]*http.Client{}}
}

// Client returns the client for a proxy (empty means the environment's
// HTTP_PROXY / NO_PROXY) and a first-byte timeout (zero means the default).
func (t *Transports) Client(proxyURL string, headerTimeout time.Duration) (*http.Client, error) {
	if headerTimeout <= 0 {
		headerTimeout = DefaultHeaderTimeout
	}
	if proxyURL == "" && headerTimeout == DefaultHeaderTimeout {
		return SharedClient(), nil
	}
	key := proxyURL + "|" + headerTimeout.String()

	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.clients[key]; ok {
		return c, nil
	}
	proxy := http.ProxyFromEnvironment
	if proxyURL != "" {
		u, err := ParseProxyURL(proxyURL)
		if err != nil {
			return nil, err
		}
		// An explicit proxy is used for everything this provider sends,
		// NO_PROXY included: it was named for this provider on purpose.
		proxy = http.ProxyURL(u)
	}
	c := &http.Client{Transport: newTransport(proxy, headerTimeout)}
	t.clients[key] = c
	return c, nil
}
