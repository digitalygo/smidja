package models

import (
	"sort"
	"strings"
)

type ModelKey struct {
	ProviderID string
	ModelID    string
}

func (k ModelKey) fullID() string {
	if strings.Contains(k.ModelID, "/") {
		return k.ModelID
	}
	if k.ProviderID == "" {
		return k.ModelID
	}
	return k.ProviderID + "/" + k.ModelID
}

func (r *Registry) GetByKey(key ModelKey) (ModelInfo, bool) {
	for _, candidate := range []string{key.fullID(), key.ModelID} {
		m, ok := r.Get(candidate)
		if !ok {
			continue
		}
		if key.ProviderID == "" || m.Provider == key.ProviderID || m.Provider == "" {
			return m, true
		}
	}
	return ModelInfo{}, false
}

func (r *Registry) RegisterKey(key ModelKey, m ModelInfo) {
	if key.ModelID == "" {
		return
	}
	r.Register(key.fullID(), m)
}

func (r *Registry) Keys() []ModelKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelKey, 0, len(r.models))
	for id, m := range r.models {
		providerID := m.Provider
		modelID := id
		if m.Provider != "" && strings.HasPrefix(id, m.Provider+"/") {
			modelID = strings.TrimPrefix(id, m.Provider+"/")
		}
		out = append(out, ModelKey{ProviderID: providerID, ModelID: modelID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}
