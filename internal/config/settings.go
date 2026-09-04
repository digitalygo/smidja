package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const bundleSettingsName = "settings.json"

const legacyBundleSettingsName = "content/settings.json"

var bundleSettingsLocations = []string{bundleSettingsName, legacyBundleSettingsName}

type RetrySettings struct {
	Enabled *bool

	MaxRetries *int

	BaseDelayMs *int64
}

type Settings struct {
	DefaultProvider string

	DefaultModel string

	SessionDir string

	Retry RetrySettings

	CompactionEnabled *bool

	ModelsCatalogURL string
}

func ParseSettings(data []byte) (*Settings, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	out := &Settings{}
	var err error
	if out.DefaultProvider, err = settingsString(fields, "defaultProvider"); err != nil {
		return nil, err
	}
	if out.DefaultModel, err = settingsString(fields, "defaultModel"); err != nil {
		return nil, err
	}
	if out.SessionDir, err = settingsString(fields, "sessionDir"); err != nil {
		return nil, err
	}
	if out.ModelsCatalogURL, err = settingsString(fields, "modelsCatalogUrl"); err != nil {
		return nil, err
	}
	if out.Retry, err = settingsRetry(fields); err != nil {
		return nil, err
	}
	if out.CompactionEnabled, err = settingsNestedBool(fields, "compaction"); err != nil {
		return nil, err
	}
	return out, nil
}

func ReadUserSettings(home string) (*Settings, error) {
	if home == "" {
		return nil, nil
	}
	return readSettingsFile(filepath.Join(home, ".smidja", "settings.json"))
}

func ReadBundleSettings(fsys fs.FS) (*Settings, error) {
	if fsys == nil {
		return nil, nil
	}
	for _, name := range bundleSettingsLocations {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			continue
		}
		out, err := ParseSettings(data)
		if err != nil {
			return nil, fmt.Errorf("settings: bundle %s: %w", name, err)
		}
		return out, nil
	}
	return nil, nil
}

func readSettingsFile(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("settings: read %s: %w", path, err)
	}
	out, err := ParseSettings(data)
	if err != nil {
		return nil, fmt.Errorf("settings: %s: %w", path, err)
	}
	return out, nil
}

func (s *Settings) envMap() map[string]string {
	if s == nil {
		return nil
	}
	m := make(map[string]string, 8)
	if s.DefaultModel != "" {
		m[envModel] = s.DefaultModel
	}
	if s.DefaultProvider != "" {
		m[envProvider] = s.DefaultProvider
	}
	if s.SessionDir != "" {
		m[envSessionDir] = s.SessionDir
	}
	if s.ModelsCatalogURL != "" {
		m[envModelsCatalogURL] = s.ModelsCatalogURL
	}
	if s.Retry.Enabled != nil {
		m[envRetry] = strconv.FormatBool(*s.Retry.Enabled)
	}
	if s.Retry.MaxRetries != nil {
		m[envRetryMaxRetries] = strconv.Itoa(*s.Retry.MaxRetries)
	}
	if s.Retry.BaseDelayMs != nil {
		m[envRetryBaseDelayMs] = strconv.FormatInt(*s.Retry.BaseDelayMs, 10)
	}
	if s.CompactionEnabled != nil {
		m[envContextEnabled] = strconv.FormatBool(*s.CompactionEnabled)
	}
	return m
}

func settingsString(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[settingsLeaf(key)]
	if !ok || jsonNull(raw) {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("field %q: want a string", key)
	}
	return s, nil
}

func settingsBool(fields map[string]json.RawMessage, key string) (*bool, error) {
	raw, ok := fields[settingsLeaf(key)]
	if !ok || jsonNull(raw) {
		return nil, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("field %q: want a boolean", key)
	}
	return &b, nil
}

func settingsNonNegativeInt(fields map[string]json.RawMessage, key string) (*int, error) {
	raw, ok := fields[settingsLeaf(key)]
	if !ok || jsonNull(raw) {
		return nil, nil
	}
	v, err := settingsNonNegativeJSONInt(raw, key)
	if err != nil {
		return nil, err
	}
	out := int(v)
	return &out, nil
}

func settingsNonNegativeInt64(fields map[string]json.RawMessage, key string) (*int64, error) {
	raw, ok := fields[settingsLeaf(key)]
	if !ok || jsonNull(raw) {
		return nil, nil
	}
	v, err := settingsNonNegativeJSONInt(raw, key)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func settingsNonNegativeJSONInt(raw json.RawMessage, key string) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !isJSONNumberStart(trimmed[0]) {
		return 0, fmt.Errorf("field %q: want a nonnegative integer", key)
	}
	var num json.Number
	if err := json.Unmarshal(trimmed, &num); err != nil {
		return 0, fmt.Errorf("field %q: want a nonnegative integer", key)
	}
	v, err := num.Int64()
	if err != nil || v < 0 {
		return 0, fmt.Errorf("field %q: want a nonnegative integer", key)
	}
	return v, nil
}

func settingsLeaf(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[i+1:]
	}
	return key
}

func isJSONNumberStart(b byte) bool {
	return b == '-' || (b >= '0' && b <= '9')
}

func settingsObject(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	raw, ok := fields[settingsLeaf(key)]
	if !ok || jsonNull(raw) {
		return nil, nil
	}
	var sub map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sub); err != nil {
		return nil, fmt.Errorf("field %q: want an object", key)
	}
	return sub, nil
}

func settingsRetry(fields map[string]json.RawMessage) (RetrySettings, error) {
	sub, err := settingsObject(fields, "retry")
	if err != nil || sub == nil {
		return RetrySettings{}, err
	}
	out := RetrySettings{}
	if out.Enabled, err = settingsBool(sub, "retry.enabled"); err != nil {
		return RetrySettings{}, err
	}
	if out.MaxRetries, err = settingsNonNegativeInt(sub, "retry.maxRetries"); err != nil {
		return RetrySettings{}, err
	}
	if out.BaseDelayMs, err = settingsNonNegativeInt64(sub, "retry.baseDelayMs"); err != nil {
		return RetrySettings{}, err
	}
	return out, nil
}

func settingsNestedBool(fields map[string]json.RawMessage, parent string) (*bool, error) {
	sub, err := settingsObject(fields, parent)
	if err != nil || sub == nil {
		return nil, err
	}
	return settingsBool(sub, parent+".enabled")
}

func jsonNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
