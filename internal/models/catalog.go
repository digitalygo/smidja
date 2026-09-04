package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultCatalogURL = "https://pi.dev/api/models"

var defaultCatalogURL = DefaultCatalogURL

const maxCatalogBody = 16 << 20

const catalogRequestTimeout = 10 * time.Second

const modelsStoreName = "models-store.json"

type CatalogSource struct {
	BaseURL string
	HTTP    *http.Client
}

func (s CatalogSource) url() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return defaultCatalogURL
}

func (s CatalogSource) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

type CatalogRecord struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	API           string          `json:"api"`
	Provider      string          `json:"provider"`
	BaseURL       string          `json:"baseUrl"`
	Reasoning     json.RawMessage `json:"reasoning"`
	Input         json.RawMessage `json:"input"`
	Cost          json.RawMessage `json:"cost"`
	ContextWindow json.RawMessage `json:"contextWindow"`
	MaxTokens     json.RawMessage `json:"maxTokens"`

	raw map[string]json.RawMessage
}

func (r *CatalogRecord) UnmarshalJSON(data []byte) error {
	type alias CatalogRecord
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	*r = CatalogRecord(a)
	r.raw = make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		switch k {
		case "id", "name", "api", "provider", "baseUrl", "reasoning", "input", "cost", "contextWindow", "maxTokens":
		default:
			r.raw[k] = v
		}
	}
	return nil
}

func (r CatalogRecord) MarshalJSON() ([]byte, error) {
	type alias CatalogRecord
	out, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.raw) == 0 {
		return out, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		return nil, err
	}
	for k, v := range r.raw {
		obj[k] = v
	}
	return json.Marshal(obj)
}

func (r CatalogRecord) Info() ModelInfo {
	fullID := r.ID
	if !strings.Contains(fullID, "/") && r.Provider != "" {
		fullID = r.Provider + "/" + fullID
	}
	provider := r.Provider
	if provider == "" {
		provider = providerOf(fullID)
	}
	return ModelInfo{
		ID:            fullID,
		ContextWindow: lenientInt64(r.ContextWindow),
		Provider:      provider,
	}
}

func Fetch(ctx context.Context, src CatalogSource) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url(), nil)
	if err != nil {
		return nil, fmt.Errorf("models: build request: %w", err)
	}
	resp, err := src.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("models: fetch %s: %w", src.url(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models: fetch %s: status %s", src.url(), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBody))
	if err != nil {
		return nil, fmt.Errorf("models: read body: %w", err)
	}
	providers, err := parseCatalog(body)
	if err != nil {
		return nil, err
	}
	var out []ModelInfo
	for _, records := range providers {
		for _, rec := range records {
			m := rec.Info()
			if m.ID == "" || m.ContextWindow <= 0 {
				continue
			}
			out = append(out, m)
		}
	}
	return out, nil
}

func parseCatalog(body []byte) (map[string]map[string]CatalogRecord, error) {
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, fmt.Errorf("models: decode catalog: %w", err)
	}
	if providers == nil {
		return nil, errors.New("models: decode catalog: expected a provider object")
	}
	out := make(map[string]map[string]CatalogRecord, len(providers))
	for pid, raw := range providers {
		var records map[string]json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			continue
		}
		if records == nil {
			continue
		}
		parsed := make(map[string]CatalogRecord, len(records))
		for mid, recordRaw := range records {
			var rec CatalogRecord
			if err := json.Unmarshal(recordRaw, &rec); err != nil {
				continue
			}
			parsed[mid] = rec
		}
		if len(parsed) > 0 {
			out[pid] = parsed
		}
	}
	return out, nil
}

type ProviderState struct {
	Models       map[string]CatalogRecord `json:"models"`
	CheckedAt    string                   `json:"checkedAt"`
	LastModified string                   `json:"lastModified"`
	ETag         string                   `json:"etag"`
}

type StoreFile struct {
	Providers map[string]ProviderState `json:"providers"`
}

func (sf StoreFile) Models() []ModelInfo {
	var out []ModelInfo
	for _, state := range sf.Providers {
		for _, rec := range state.Models {
			m := rec.Info()
			if m.ID == "" || m.ContextWindow <= 0 {
				continue
			}
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func LoadStore(path string) (StoreFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StoreFile{Providers: map[string]ProviderState{}}, nil
		}
		return StoreFile{}, fmt.Errorf("models: read store %s: %w", path, err)
	}
	var sf StoreFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return StoreFile{}, fmt.Errorf("models: decode store %s: %w", path, err)
	}
	if sf.Providers == nil {
		sf.Providers = map[string]ProviderState{}
	}
	return sf, nil
}

func StorePathFor(sessionDir string) string {
	return filepath.Join(filepath.Dir(sessionDir), modelsStoreName)
}

func (s *CatalogSource) RefreshTo(path string) error {
	if offlineMode() {
		return nil
	}
	cached, err := LoadStore(path)
	if err != nil {
		cached = StoreFile{Providers: map[string]ProviderState{}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), catalogRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(), nil)
	if err != nil {
		return fmt.Errorf("models: build request: %w", err)
	}
	etag, lastModified := cached.conditionals()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("models: refresh %s: %w", s.url(), err)
	}
	defer resp.Body.Close()
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	switch resp.StatusCode {
	case http.StatusNotModified:
		for pid, state := range cached.Providers {
			state.CheckedAt = checkedAt
			cached.Providers[pid] = state
		}
		return cached.write(path)
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBody))
		if err != nil {
			return fmt.Errorf("models: read body: %w", err)
		}
		providers, err := parseCatalog(body)
		if err != nil {
			return err
		}
		fresh := StoreFile{Providers: make(map[string]ProviderState, len(providers))}
		for pid, records := range providers {
			fresh.Providers[pid] = ProviderState{
				Models:       records,
				CheckedAt:    checkedAt,
				LastModified: resp.Header.Get("Last-Modified"),
				ETag:         resp.Header.Get("ETag"),
			}
		}
		return fresh.write(path)
	default:
		return fmt.Errorf("models: refresh %s: status %s", s.url(), resp.Status)
	}
}

func (sf StoreFile) conditionals() (etag, lastModified string) {
	ids := make([]string, 0, len(sf.Providers))
	for pid := range sf.Providers {
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	for _, pid := range ids {
		state := sf.Providers[pid]
		if etag == "" {
			etag = state.ETag
		}
		if lastModified == "" {
			lastModified = state.LastModified
		}
		if etag != "" && lastModified != "" {
			break
		}
	}
	return etag, lastModified
}

func (sf StoreFile) write(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("models: create %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("models: encode store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".models-*.tmp")
	if err != nil {
		return fmt.Errorf("models: create temp: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("models: chmod temp: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("models: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("models: close temp: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("models: rename %s: %w", path, err)
	}
	return nil
}

func (r *Registry) MergeRefreshed(store StoreFile, local []ModelInfo) {
	r.Merge(store.Models())
	r.Merge(local)
}

func LoadLocalOverrides(path string) ([]ModelInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseLocalOverrides(data)
}

func ParseOverrides(data []byte) ([]ModelInfo, error) {
	return parseLocalOverrides(data)
}

func parseLocalOverrides(data []byte) ([]ModelInfo, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []wireModel
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("models: decode local overrides: %w", err)
		}
		var out []ModelInfo
		for _, w := range list {
			m := w.info()
			if m.ID == "" {
				continue
			}
			out = append(out, m)
		}
		return out, nil
	}
	providers, err := parseCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("models: decode local overrides: %w", err)
	}
	var out []ModelInfo
	for _, records := range providers {
		for _, rec := range records {
			m := rec.Info()
			if m.ID == "" {
				continue
			}
			out = append(out, m)
		}
	}
	return out, nil
}

func LocalOverridesPath(workspaceRoot, home string) string {
	candidates := []string{
		filepath.Join(workspaceRoot, ".smidja", "models.json"),
		filepath.Join(home, ".smidja", "models.json"),
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func offlineMode() bool {
	return envTruthy(os.Getenv("SMIDJA_OFFLINE"))
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
