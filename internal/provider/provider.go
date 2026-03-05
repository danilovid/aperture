package provider

import (
	"context"
	"io"
)

// Provider defines the interface for LLM providers.
type Provider interface {
	// Models returns the list of available models. Caller must close the returned ReadCloser.
	Models(ctx context.Context) (io.ReadCloser, string, int, error)
	// ChatCompletions proxies the request to the provider. Caller must close the returned ReadCloser.
	ChatCompletions(ctx context.Context, body io.Reader, contentType string) (io.ReadCloser, string, int, error)
}
