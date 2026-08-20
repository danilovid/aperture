package ner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc, tune func(*Config)) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := Config{URL: srv.URL, Timeout: 2 * time.Second}
	if tune != nil {
		tune(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDetectSendsOneBatchAndKeepsOrder(t *testing.T) {
	var got scanRequest
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/scan" || r.Method != http.MethodPost {
			t.Errorf("called %s %s, want POST /scan", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Write([]byte(`{"results":[
			{"spans":[{"start":0,"end":11,"label":"PERSON","score":0.99}]},
			{"spans":[]},
			{"spans":[{"start":3,"end":9,"label":"LOC","score":0.8}]}]}`))
	}, nil)

	spans, err := c.Detect(context.Background(), []string{"Ivan Petrov", "nothing here", "in Moscow"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Texts) != 3 {
		t.Errorf("sent %d texts, want one batch of 3", len(got.Texts))
	}
	if len(spans) != 3 || len(spans[0]) != 1 || len(spans[1]) != 0 || len(spans[2]) != 1 {
		t.Fatalf("spans = %+v", spans)
	}
	if spans[0][0].Label != "PERSON" || spans[2][0].Label != "LOC" {
		t.Errorf("labels lost: %+v", spans)
	}
}

// A minimal service may answer the single-text shape; that must still work.
func TestDetectAcceptsSingleTextShape(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"spans":[{"start":0,"end":11,"label":"PERSON","score":0.9}]}`))
	}, nil)

	spans, err := c.Detect(context.Background(), []string{"Ivan Petrov"})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || len(spans[0]) != 1 {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestDetectDropsWhatTheGatewayWillNotActOn(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"spans":[
			{"start":0,"end":11,"label":"PERSON","score":0.2},
			{"start":0,"end":11,"label":"ORG","score":0.99},
			{"start":-1,"end":5,"label":"PERSON","score":0.99},
			{"start":0,"end":9999,"label":"PERSON","score":0.99},
			{"start":5,"end":5,"label":"PERSON","score":0.99},
			{"start":0,"end":11,"label":"PERSON","score":0.95}]}]}`))
	}, func(c *Config) { c.MinScore = 0.5 })

	spans, err := c.Detect(context.Background(), []string{"Ivan Petrov"})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans[0]) != 1 || spans[0][0].Score != 0.95 {
		t.Errorf("kept %+v, want only the confident in-range PERSON span", spans[0])
	}
}

// Cutting a multi-byte character in half would corrupt the redacted text.
func TestDetectRejectsSpansInsideARune(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"spans":[{"start":1,"end":5,"label":"PERSON","score":0.99}]}]}`))
	}, nil)

	spans, err := c.Detect(context.Background(), []string{"Иван Петров"})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans[0]) != 0 {
		t.Errorf("accepted a span starting mid-character: %+v", spans[0])
	}
}

func TestDetectReportsServiceFailures(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, nil)

	if _, err := c.Detect(context.Background(), []string{"Ivan Petrov"}); err == nil {
		t.Error("a 500 from the model service was reported as success")
	}
}

func TestDetectHonoursTheTimeout(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(`{"results":[{"spans":[]}]}`))
	}, func(c *Config) { c.Timeout = 30 * time.Millisecond })

	start := time.Now()
	if _, err := c.Detect(context.Background(), []string{"Ivan Petrov"}); err == nil {
		t.Error("a slow service was not cut off")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("waited %v, want the call bounded by the timeout", elapsed)
	}
}

// The point of a local model is that prompt text never leaves the network.
func TestNewRefusesAPublicService(t *testing.T) {
	// A public address, and a name that cannot be verified as local: both are
	// refused, and both must say how to override it on purpose.
	for _, u := range []string{"https://8.8.8.8", "https://ner.invalid"} {
		err := errFrom(New(Config{URL: u}))
		if err == nil {
			t.Fatalf("%s was accepted", u)
		}
		if !strings.Contains(err.Error(), "NER_ALLOW_REMOTE") {
			t.Errorf("%s: the error should say how to override it deliberately: %v", u, err)
		}
		if _, err := New(Config{URL: u, AllowRemote: true}); err != nil {
			t.Errorf("%s: explicit opt-in was still refused: %v", u, err)
		}
	}
}

func errFrom(_ *Client, err error) error { return err }

func TestNewAcceptsLocalAddresses(t *testing.T) {
	for _, u := range []string{
		"http://localhost:8081",
		"http://127.0.0.1:8081",
		"http://10.1.2.3:8081",
		"http://192.168.1.10",
		"http://172.16.0.9:8081",
	} {
		if _, err := New(Config{URL: u}); err != nil {
			t.Errorf("%s was refused: %v", u, err)
		}
	}
}

func TestNewRejectsNonsense(t *testing.T) {
	for _, u := range []string{"", "ftp://localhost", "not a url"} {
		if _, err := New(Config{URL: u}); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
}
