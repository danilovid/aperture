package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// In production the gateway sits behind Caddy, and Caddy proxies a list of
// prefixes. Anything not on that list falls through to the console, so a new
// endpoint added here and not there answers a React page to an API call —
// which is a confusing way to find out about a deploy, usually from a user.
// The site file is versioned with the code so this test can exist.
func TestEveryRouteIsProxiedInProduction(t *testing.T) {
	site, err := os.ReadFile(filepath.Join("..", "..", "deploy", "aperture.caddy"))
	if err != nil {
		t.Fatalf("read the Caddy site file: %v", err)
	}
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}

	var prefixes []string
	for _, m := range regexp.MustCompile(`handle (\S+) \{`).FindAllStringSubmatch(string(site), -1) {
		prefixes = append(prefixes, m[1])
	}
	if len(prefixes) == 0 {
		t.Fatal("no handle blocks found; this test is looking in the wrong place")
	}

	routes := regexp.MustCompile(`mux\.HandleFunc\("(\S+) (\S+)"`).FindAllStringSubmatch(string(src), -1)
	if len(routes) == 0 {
		t.Fatal("no routes found; this test is looking in the wrong place")
	}

	for _, r := range routes {
		method, pattern := r[1], r[2]
		// A path parameter matches anything, so substitute something that
		// cannot accidentally match a prefix.
		path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(pattern, "x")
		if proxiedBy(prefixes, path) == "" {
			t.Errorf("%s %s is not proxied: in production it would reach the console, "+
				"not the gateway — add it to deploy/aperture.caddy", method, pattern)
		}
	}
}

func proxiedBy(prefixes []string, path string) string {
	for _, p := range prefixes {
		if strings.HasSuffix(p, "*") && strings.HasPrefix(path, strings.TrimSuffix(p, "*")) {
			return p
		}
		if p == path {
			return p
		}
	}
	return ""
}
