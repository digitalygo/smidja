package manifest

import (
	"errors"

	"github.com/digitalygo/smidja/internal/providers"
)

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
