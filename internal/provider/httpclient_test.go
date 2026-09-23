package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestTransportsAreBuiltOncePerProxy(t *testing.T) {
	tr := NewTransports()
	a1, err := tr.Client("http://proxy-a.internal:3128", 0)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := tr.Client("http://proxy-a.internal:3128", 0)
	b, _ := tr.Client("http://proxy-b.internal:3128", 0)
	if a1 != a2 {
		t.Error("the same proxy built two clients: every request would get a fresh connection pool")
	}
	if a1 == b {
		t.Error("two proxies share one client")
	}
	if c, _ := tr.Client("", 0); c != SharedClient() {
		t.Error("no proxy and no timeout should be the shared client")
	}
	if slow, _ := tr.Client("http://proxy-a.internal:3128", 5*time.Minute); slow == a1 {
		t.Error("a different timeout shared a transport with the default one")
	}
}

func TestProxyURLsAreChecked(t *testing.T) {
	for _, bad := range []string{"proxy.internal:3128", "ftp://proxy.internal", "http://", ""} {
		if _, err := ParseProxyURL(bad); err == nil {
			t.Errorf("%q was accepted as a proxy", bad)
		}
	}
	for _, good := range []string{"http://proxy:3128", "https://user:pass@proxy.corp:443", "socks5://127.0.0.1:1080"} {
		if _, err := ParseProxyURL(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
}

// The point of a per-provider proxy: that provider's traffic actually goes
// through it, whatever the environment says.
func TestRequestsGoThroughTheNamedProxy(t *testing.T) {
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A request through a proxy carries the absolute target URL.
		seen = append(seen, r.URL.String())
		w.WriteHeader(http.StatusTeapot)
	}))
	defer proxy.Close()

	c, err := NewTransports().Client(proxy.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get("http://upstream.invalid/v1/models")
	if err != nil {
		t.Fatalf("request through the proxy failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot || len(seen) != 1 || seen[0] != "http://upstream.invalid/v1/models" {
		t.Fatalf("the request did not go through the proxy: status %d, proxy saw %v", resp.StatusCode, seen)
	}
}
