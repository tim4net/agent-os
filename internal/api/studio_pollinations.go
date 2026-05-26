package api

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
)

// PollinationsProvider implements StudioProvider using the free Pollinations API.
// No API key is required — it generates images on-fetch via a simple GET URL.
type PollinationsProvider struct{}

// NewPollinationsProvider creates a new PollinationsProvider.
func NewPollinationsProvider() *PollinationsProvider {
	return &PollinationsProvider{}
}

// Generate returns a Pollinations image URL. The image is generated on-fetch,
// so no POST request is needed — we just construct the URL.
func (p *PollinationsProvider) Generate(ctx context.Context, prompt string, genType string, model string) (string, error) {
	if genType != "" && genType != "image" {
		return "", fmt.Errorf("pollinations: unsupported generation type: %s", genType)
	}

	encoded := url.PathEscape(prompt)
	seed := rand.Int63()

	resultURL := fmt.Sprintf("https://image.pollinations.ai/prompt/%s?width=1024&height=1024&nologo=true&seed=%d", encoded, seed)
	return resultURL, nil
}
