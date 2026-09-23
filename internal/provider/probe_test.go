package provider

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOpenAI answers the model list for one key.
func fakeOpenAI(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// closedAddr is an address nothing listens on.
func closedAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestProbeStages(t *testing.T) {
	up := fakeOpenAI(t)
	ctx := context.Background()

	ok := Probe(ctx, ProbeRequest{Kind: "openai", BaseURL: up.URL, APIKey: "sk-good"})
	if !ok.OK || ok.Stage != StageOK || ok.Status != 200 {
		t.Errorf("a good key: %+v", ok)
	}

	bad := Probe(ctx, ProbeRequest{Kind: "openai", BaseURL: up.URL, APIKey: "sk-wrong"})
	if bad.OK || bad.Stage != StageAuth || !strings.Contains(bad.Message, "refused") {
		t.Errorf("a wrong key: %+v", bad)
	}

	wrong := Probe(ctx, ProbeRequest{Kind: "openai-compatible", BaseURL: up.URL + "/nowhere", APIKey: "sk-good"})
	if wrong.OK || wrong.Stage != StageHTTP || wrong.Status != 404 {
		t.Errorf("a wrong address: %+v", wrong)
	}

	down := Probe(ctx, ProbeRequest{Kind: "openai", BaseURL: "http://" + closedAddr(t), APIKey: "sk-good"})
	if down.OK || down.Stage != StageConnect {
		t.Errorf("a host that is down: %+v", down)
	}
}

func TestProbeBlamesAProxyThatIsNotThere(t *testing.T) {
	up := fakeOpenAI(t)
	// An HTTPS target makes the client open a CONNECT tunnel, which is
	// where a missing proxy shows up as "proxyconnect".
	tlsUp := httptest.NewTLSServer(up.Config.Handler)
	defer tlsUp.Close()

	res := Probe(context.Background(), ProbeRequest{
		Kind: "openai", BaseURL: tlsUp.URL, APIKey: "sk-good",
		ProxyURL: "http://" + closedAddr(t),
	})
	if res.OK || res.Stage != StageProxy || res.Via == "" {
		t.Errorf("a dead proxy was not blamed: %+v", res)
	}
}

func TestProbeGoesThroughAWorkingProxy(t *testing.T) {
	up := fakeOpenAI(t)
	var relayed int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayed++
		// A plain-HTTP forward proxy: pass the absolute request on.
		req, _ := http.NewRequest(r.Method, r.URL.String(), nil)
		req.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	defer proxy.Close()

	res := Probe(context.Background(), ProbeRequest{Kind: "openai", BaseURL: up.URL, APIKey: "sk-good", ProxyURL: proxy.URL})
	if !res.OK || relayed != 1 || res.Via == "" {
		t.Errorf("the probe did not go through the proxy: %+v, relayed %d", res, relayed)
	}
}

func TestProbeRefusesAnUnusableProxyAddress(t *testing.T) {
	res := Probe(context.Background(), ProbeRequest{Kind: "openai", APIKey: "k", ProxyURL: "ftp://proxy"})
	if res.OK || res.Stage != StageProxy {
		t.Errorf("an ftp proxy was tried: %+v", res)
	}
}

func TestProxyPasswordsAreNeverShown(t *testing.T) {
	got := RedactProxy("http://svc-aperture:hunter2@proxy.corp:3128")
	if strings.Contains(got, "hunter2") || got != "http://svc-aperture@proxy.corp:3128" {
		t.Errorf("RedactProxy = %q", got)
	}
	res := Probe(context.Background(), ProbeRequest{
		Kind: "openai", BaseURL: "http://" + closedAddr(t), APIKey: "k",
		ProxyURL: "http://user:hunter2@" + closedAddr(t),
	})
	if strings.Contains(res.Message+res.Via+res.Target, "hunter2") {
		t.Errorf("a probe result showed the proxy password: %+v", res)
	}
}
