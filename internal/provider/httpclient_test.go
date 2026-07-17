package provider

import (
	"net/http"
	"testing"
)

// The upstream client must honor HTTP(S)_PROXY / NO_PROXY so Aperture works in
// networks that force egress through a corporate proxy. A hand-built Transport
// defaults Proxy to nil, so this guards against that regression.
func TestHTTPClientHonorsProxyEnv(t *testing.T) {
	tr, ok := NewHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport is not *http.Transport")
	}
	if tr.Proxy == nil {
		t.Fatal("transport.Proxy is nil — HTTPS_PROXY/NO_PROXY would be ignored")
	}

	// Behavioral check: with a proxy configured, the resolver returns it.
	// (http.ProxyFromEnvironment caches env on first use, so use a fresh
	// httpproxy-style resolver only if not already cached; asserting non-nil
	// above is the deterministic guarantee — this is a best-effort extra.)
	t.Setenv("HTTPS_PROXY", "http://corp-proxy.internal:3128")
	req, _ := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if u, err := tr.Proxy(req); err == nil && u != nil && u.Host != "corp-proxy.internal:3128" {
		t.Errorf("proxy resolved to unexpected host: %s", u.Host)
	}
}
