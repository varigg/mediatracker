package providers

import (
	"fmt"

	"github.com/varigg/mediatracker/internal/store"
)

// Registry maps a media type to its MetadataProvider. The HTTP layer
// resolves providers exclusively through it and never names an upstream.
type Registry struct {
	m map[store.MediaType]MetadataProvider
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[store.MediaType]MetadataProvider)}
}

func (r *Registry) Register(mt store.MediaType, p MetadataProvider) {
	r.m[mt] = p
}

// Get returns the provider for mt. The error names the missing type so
// the HTTP layer can render it as a 4xx.
func (r *Registry) Get(mt store.MediaType) (MetadataProvider, error) {
	p, ok := r.m[mt]
	if !ok {
		return nil, fmt.Errorf("no metadata provider registered for media type %q", mt)
	}
	return p, nil
}
