package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Probe checks, before anybody relies on it, that a provider can be reached
// the way it is configured: through its proxy, at its address, with its key.
// Finding out on the first real request means finding out from an agent that
// has already failed.
//
// It makes one cheap, read-only call — the model list where there is one —
// and says which step failed, because "it didn't work" is not something a
// person can fix, and "the proxy refused the connection" is.

// ProbeStage is how far a probe got.
type ProbeStage string

const (
	StageProxy   ProbeStage = "proxy"   // the proxy could not be reached, or refused the tunnel
	StageConnect ProbeStage = "connect" // the provider's host could not be reached
	StageTLS     ProbeStage = "tls"     // reached, but the certificate was not trusted
	StageAuth    ProbeStage = "auth"    // reached, and the key was refused
	StageHTTP    ProbeStage = "http"    // reached, and it answered something unexpected
	StageOK      ProbeStage = "ok"
)

// ProbeResult is what a person is shown after pressing "Test connection".
type ProbeResult struct {
	OK        bool       `json:"ok"`
	Stage     ProbeStage `json:"stage"`
	Status    int        `json:"status,omitempty"`
	LatencyMS int64      `json:"latency_ms"`
	// Target is the address probed, without credentials.
	Target string `json:"target"`
	// Via is the proxy's host, when there is one, without credentials.
	Via     string `json:"via,omitempty"`
	Message string `json:"message"`
}

// ProbeRequest is what to probe. Kind is one of storage's provider kinds; it
// is a string here so this package does not depend on storage.
type ProbeRequest struct {
	Kind     string
	BaseURL  string
	APIKey   string
	ProxyURL string
}

// defaultBases are the documented addresses, for a probe with none given.
var defaultBases = map[string]string{
	"openai":    "https://api.openai.com",
	"anthropic": "https://api.anthropic.com",
	"groq":      "https://api.groq.com/openai/v1",
	"jev":       "https://www.jevai.org",
}

// Probe runs one check. It builds its own transport rather than borrowing a
// cached one: what is being tested is often a proxy nobody has saved yet, and
// it has no business in the cache until they do.
func Probe(ctx context.Context, req ProbeRequest) ProbeResult {
	base := strings.TrimSuffix(req.BaseURL, "/")
	if base == "" {
		base = defaultBases[req.Kind]
	}
	target, authHeader, authValue, keyChecked := probeTarget(req.Kind, base, req.APIKey)

	res := ProbeResult{Target: redact(target)}
	proxy := http.ProxyFromEnvironment
	if req.ProxyURL != "" {
		u, err := ParseProxyURL(req.ProxyURL)
		if err != nil {
			res.Stage, res.Message = StageProxy, err.Error()
			return res
		}
		proxy = http.ProxyURL(u)
		res.Via = u.Host
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Transport: newTransport(proxy, 12*time.Second)}
	defer client.CloseIdleConnections()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		res.Stage, res.Message = StageConnect, "not a usable address: "+err.Error()
		return res
	}
	if authHeader != "" {
		httpReq.Header.Set(authHeader, authValue)
	}
	if req.Kind == "anthropic" {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Stage, res.Message = classify(err, res.Via)
		return res
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	res.Status = resp.StatusCode

	host := hostOf(target)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.Stage = StageAuth
		res.Message = "reached " + host + ", and the key was refused (" + http.StatusText(resp.StatusCode) + ")"
	case resp.StatusCode == http.StatusProxyAuthRequired:
		res.Stage = StageProxy
		res.Message = "the proxy wants credentials: put them in its address, http://user:password@host:port"
	case resp.StatusCode >= 200 && resp.StatusCode < 300 && keyChecked:
		res.OK, res.Stage = true, StageOK
		res.Message = "reached " + host + " and the key was accepted"
	case resp.StatusCode < 500 && !keyChecked:
		// Jev has nothing to read without spending a decision, so all this
		// can honestly say is that the address answers.
		res.OK, res.Stage = true, StageOK
		res.Message = "reached " + host + "; the key is checked on the first real request"
	case resp.StatusCode == http.StatusNotFound:
		res.Stage = StageHTTP
		res.Message = "reached " + host + ", but nothing answers at " + redact(target) + " — check the address"
	default:
		res.Stage = StageHTTP
		res.Message = "reached " + host + ", and it answered " + resp.Status
	}
	return res
}

func probeTarget(kind, base, key string) (target, header, value string, keyChecked bool) {
	switch kind {
	case "openai":
		return base + "/v1/models", "Authorization", "Bearer " + key, true
	case "anthropic":
		return base + "/v1/models", "x-api-key", key, true
	case "jev":
		return base + "/", "", "", false
	default: // groq and every OpenAI-compatible provider: the version is in the base
		return base + "/models", "Authorization", "Bearer " + key, true
	}
}

// classify turns a transport error into the step that failed.
func classify(err error, via string) (ProbeStage, string) {
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var opErr *net.OpError
	var dnsErr *net.DNSError
	msg := err.Error()

	switch {
	case via != "" && strings.Contains(msg, "proxyconnect"):
		return StageProxy, "could not get through the proxy " + via + ": " + rootCause(err)
	case errors.As(err, &certErr), errors.As(err, &unknownAuthority), errors.As(err, &hostnameErr),
		strings.Contains(msg, "x509:"), strings.Contains(msg, "tls:"):
		return StageTLS, "reached the host, but its certificate is not trusted: " + rootCause(err) +
			" — an intercepting proxy needs its CA in the system trust store"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout"):
		if via != "" {
			return StageConnect, "no answer within 15 seconds through " + via
		}
		return StageConnect, "no answer within 15 seconds — the host may be blocked from this network"
	case errors.As(err, &dnsErr):
		return StageConnect, "the host name does not resolve: " + dnsErr.Name
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return StageConnect, "could not connect: " + rootCause(err)
	default:
		return StageConnect, rootCause(err)
	}
}

func rootCause(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}
		err = next
	}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// redact drops any credentials from an address before it is shown.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// RedactProxy shows a proxy address without its password — enough to tell
// which proxy it is.
func RedactProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u.Scheme + "://" + func() string {
		if u.User != nil {
			return u.User.Username() + "@"
		}
		return ""
	}() + u.Host
}
