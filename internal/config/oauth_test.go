package config

import "testing"

func TestOAuthProvidersNeedBothHalves(t *testing.T) {
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "id-only")
	if _, err := loadOAuth(); err == nil {
		t.Error("a client id without its secret was accepted")
	}
}

func TestOAuthProvidersAreOffUntilConfigured(t *testing.T) {
	got, err := loadOAuth()
	if err != nil || len(got) != 0 {
		t.Errorf("providers with nothing configured: %v %v", got, err)
	}
}

func TestOAuthProviderEndpointsCanBeOverridden(t *testing.T) {
	t.Setenv("OAUTH_GITHUB_CLIENT_ID", "id")
	t.Setenv("OAUTH_GITHUB_CLIENT_SECRET", "secret")
	t.Setenv("OAUTH_GITHUB_TOKEN_URL", "https://github.internal/login/oauth/access_token")
	got, err := loadOAuth()
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	p := got[0]
	if p.ID != "github" || p.TokenURL != "https://github.internal/login/oauth/access_token" {
		t.Errorf("override not applied: %+v", p)
	}
	if p.AuthURL != "https://github.com/login/oauth/authorize" {
		t.Errorf("an endpoint that was not overridden changed: %s", p.AuthURL)
	}
}

func TestPublicURLMustBeAnOrigin(t *testing.T) {
	for _, bad := range []string{"aperture.example.com", "ftp://x.example", "https://x.example/console", "https://"} {
		t.Setenv("PUBLIC_URL", bad)
		if _, err := loadPublicURL(); err == nil {
			t.Errorf("PUBLIC_URL %q was accepted", bad)
		}
	}
	t.Setenv("PUBLIC_URL", "https://aperture.example.com/")
	got, err := loadPublicURL()
	if err != nil || got != "https://aperture.example.com" {
		t.Errorf("a good PUBLIC_URL came back as %q, %v", got, err)
	}
}
