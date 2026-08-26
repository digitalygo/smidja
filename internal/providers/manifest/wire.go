package manifest

import (
	"errors"

	"github.com/digitalygo/smidja/internal/providers"
)

// Wire registers a driver for every spec in All that Build can
// construct without error, so a single call wires the whole manifest.
// Specs whose mandatory environment is unset at build time (the
// cloudflare account/gateway ids, the cloudflare AI Gateway key, and
// the azure resource endpoint) are skipped rather than failing the
// wiring: the harness must run with only the providers the user
// configured. The registry is only written for specs that built
// successfully; skipped returns the ids of the skipped specs in All
// order. A nil registry returns an error.
func Wire(reg *providers.Registry, deps Deps) (skipped []string, err error) {
	if reg == nil {
		return nil, errors.New("manifest: nil registry")
	}
	for _, spec := range All {
		d, buildErr := Build(spec.ID, deps)
		if buildErr != nil {
			skipped = append(skipped, spec.ID)
			continue
		}
		reg.Register(spec.ID, d)
	}
	return skipped, nil
}
