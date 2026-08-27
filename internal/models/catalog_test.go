package models

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pi-models-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type catalogServer struct {
	t                *testing.T
	body             string
	etag             string
	status           int
	calls            int
	lastIfNone       string
	lastIfMod        string
	notModified      bool
	notModifiedCalls int
}

func newCatalogServer(t *testing.T, body string) *catalogServer {
	return &catalogServer{t: t, body: body, etag: `"v1"`, status: http.StatusOK}
}

func (s *catalogServer) handler(w http.ResponseWriter, r *http.Request) {
	s.calls++
	s.lastIfNone = r.Header.Get("If-None-Match")
	s.lastIfMod = r.Header.Get("If-Modified-Since")
	if s.notModified && s.lastIfNone == s.etag {
		s.notModifiedCalls++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", s.etag)
	w.Header().Set("Last-Modified", "Wed, 01 Jan 2025 00:00:00 GMT")
	w.WriteHeader(s.status)
	if s.status == http.StatusOK {
		io.WriteString(w, s.body)
	}
}

func (s *catalogServer) start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(s.handler))
}

func TestFetchUnifiedCatalog(t *testing.T) {
	cs := newCatalogServer(t, readFixture(t))
	srv := cs.start()
	defer srv.Close()
	got, err := Fetch(context.Background(), CatalogSource{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 20 {
		t.Fatalf("got %d entries, want 20", len(got))
	}
	byID := make(map[string]ModelInfo)
	for _, m := range got {
		byID[m.ID] = m
	}
	fable, ok := byID["anthropic/claude-fable-5"]
	if !ok {
		t.Fatal("missing anthropic/claude-fable-5")
	}
	if fable.ContextWindow != 1_000_000 || fable.Provider != "anthropic" {
		t.Errorf("fable = %+v", fable)
	}
	pro, ok := byID["deepseek/deepseek-v4-pro"]
	if !ok || pro.ContextWindow != 1_000_000 {
		t.Errorf("deepseek-v4-pro = %+v ok=%v", pro, ok)
	}
	if _, ok := byID["openai/gpt-4.1"]; !ok {
		t.Error("missing openai/gpt-4.1")
	}
	if _, ok := byID["openai/gpt-4"]; !ok {
		t.Error("missing openai/gpt-4")
	}
}

func TestFetchUnifiedCatalogServerError(t *testing.T) {
	srv := newCatalogServer(t, "boom")
	srv.status = http.StatusInternalServerError
	server := srv.start()
	defer server.Close()
	if _, err := Fetch(context.Background(), CatalogSource{BaseURL: server.URL}); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestFetchUnifiedCatalogMalformedTopLevel(t *testing.T) {
	for _, body := range []string{"not json", "[]", "null"} {
		srv := newCatalogServer(t, body).start()
		_, err := Fetch(context.Background(), CatalogSource{BaseURL: srv.URL})
		srv.Close()
		if err == nil {
			t.Errorf("Fetch with body %q succeeded, want an error", body)
		}
	}
}

func TestFetchUnifiedCatalogSkipsBadProvidersAndRecords(t *testing.T) {
	body := `{
	  "good": {"m1": {"id": "m1", "provider": "good", "contextWindow": 1000}},
	  "array-provider": [1, 2],
	  "broken": {"x": "not-an-object", "y": 42},
	  "empty": {}
	}`
	srv := newCatalogServer(t, body).start()
	defer srv.Close()
	got, err := Fetch(context.Background(), CatalogSource{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good/m1" {
		t.Errorf("got %+v, want only good/m1", got)
	}
}

func TestFetchUnifiedCatalogEmptyObject(t *testing.T) {
	srv := newCatalogServer(t, `{}`).start()
	defer srv.Close()
	got, err := Fetch(context.Background(), CatalogSource{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestCatalogRecordRawRoundTrip(t *testing.T) {
	var rec CatalogRecord
	raw := `{"id":"m","name":"M","api":"x","provider":"p","baseUrl":"https://e","reasoning":true,"input":["text"],"cost":{"input":1,"output":2},"contextWindow":123,"maxTokens":456,"thinkingLevelMap":{"off":null},"compat":{"supportsStrictTools":true}}`
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rec.ID != "m" || rec.Name != "M" || rec.ContextWindow.String() != "123" {
		t.Errorf("rec = %+v", rec)
	}
	if _, ok := rec.raw["thinkingLevelMap"]; !ok {
		t.Error("unknown field thinkingLevelMap not preserved")
	}
	if _, ok := rec.raw["compat"]; !ok {
		t.Error("unknown field compat not preserved")
	}
	if _, ok := rec.raw["id"]; ok {
		t.Error("known field id leaked into raw")
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["thinkingLevelMap"]; !ok {
		t.Error("round trip lost thinkingLevelMap")
	}
	if _, ok := obj["compat"]; !ok {
		t.Error("round trip lost compat")
	}
	if _, ok := obj["cost"]; !ok {
		t.Error("round trip lost cost")
	}
}

func TestCatalogRecordInfoPrefixesID(t *testing.T) {
	m := (CatalogRecord{ID: "claude-fable-5", Provider: "anthropic", ContextWindow: json.RawMessage(`200000`)}).Info()
	if m.ID != "anthropic/claude-fable-5" || m.Provider != "anthropic" || m.ContextWindow != 200_000 {
		t.Errorf("Info = %+v", m)
	}
	already := (CatalogRecord{ID: "openai/gpt-5", Provider: "openai", ContextWindow: json.RawMessage(`400000`)}).Info()
	if already.ID != "openai/gpt-5" {
		t.Errorf("already-prefixed Info = %+v", already)
	}
	noProvider := (CatalogRecord{ID: "bare/model", ContextWindow: json.RawMessage(`7`)}).Info()
	if noProvider.Provider != "bare" {
		t.Errorf("derived provider Info = %+v", noProvider)
	}
}

func TestRefreshToWritesStore(t *testing.T) {
	srv := newCatalogServer(t, readFixture(t)).start()
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models-store.json")
	src := &CatalogSource{BaseURL: srv.URL}
	if err := src.RefreshTo(path); err != nil {
		t.Fatalf("RefreshTo: %v", err)
	}
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if len(store.Providers) != 3 {
		t.Fatalf("providers = %v, want 3", providerKeys(store))
	}
	anthropic := store.Providers["anthropic"]
	if len(anthropic.Models) != 13 {
		t.Errorf("anthropic models = %d, want 13", len(anthropic.Models))
	}
	if anthropic.ETag != `"v1"` || anthropic.CheckedAt == "" {
		t.Errorf("anthropic state = %+v", anthropic)
	}
	if _, ok := anthropic.Models["claude-fable-5"]; !ok {
		t.Error("claude-fable-5 missing from store")
	}
}

func TestRefreshTo304KeepsCacheAndUpdatesCheckedAt(t *testing.T) {
	cs := newCatalogServer(t, readFixture(t))
	srv := cs.start()
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models-store.json")
	src := &CatalogSource{BaseURL: srv.URL}
	if err := src.RefreshTo(path); err != nil {
		t.Fatalf("first RefreshTo: %v", err)
	}
	before, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	firstChecked := before.Providers["anthropic"].CheckedAt
	cs.notModified = true
	if err := src.RefreshTo(path); err != nil {
		t.Fatalf("second RefreshTo: %v", err)
	}
	after, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Providers["anthropic"].Models) != 13 {
		t.Error("304 refresh dropped the cached models")
	}
	if after.Providers["anthropic"].CheckedAt == firstChecked {
		t.Error("checkedAt did not advance on 304")
	}
	if cs.notModifiedCalls != 1 {
		t.Errorf("server saw %d 304-triggering requests, want 1", cs.notModifiedCalls)
	}
}

func TestRefreshToSendsConditionalHeaders(t *testing.T) {
	cs := newCatalogServer(t, readFixture(t))
	srv := cs.start()
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models-store.json")
	src := &CatalogSource{BaseURL: srv.URL}
	if err := src.RefreshTo(path); err != nil {
		t.Fatal(err)
	}
	if cs.lastIfNone != "" {
		t.Error("first request must not carry a conditional header")
	}
	if err := src.RefreshTo(path); err != nil {
		t.Fatal(err)
	}
	if cs.lastIfNone != `"v1"` {
		t.Errorf("If-None-Match = %q, want %q", cs.lastIfNone, `"v1"`)
	}
	if cs.lastIfMod == "" {
		t.Error("If-Modified-Since must be sent")
	}
}

func TestRefreshToServerErrorKeepsCache(t *testing.T) {
	srv := newCatalogServer(t, readFixture(t)).start()
	path := filepath.Join(t.TempDir(), "models-store.json")
	src := &CatalogSource{BaseURL: srv.URL}
	if err := src.RefreshTo(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	srv.Close()
	bad := newCatalogServer(t, "boom")
	bad.status = http.StatusInternalServerError
	badServer := bad.start()
	defer badServer.Close()
	src.BaseURL = badServer.URL
	if err := src.RefreshTo(path); err == nil {
		t.Fatal("expected an error for a 500 refresh")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("failed refresh replaced the last-known-good store")
	}
}

func TestRefreshToMalformedBodyKeepsCache(t *testing.T) {
	srv := newCatalogServer(t, readFixture(t)).start()
	path := filepath.Join(t.TempDir(), "models-store.json")
	src := &CatalogSource{BaseURL: srv.URL}
	if err := src.RefreshTo(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	srv.Close()
	bad := newCatalogServer(t, "definitely not json")
	badServer := bad.start()
	defer badServer.Close()
	src.BaseURL = badServer.URL
	if err := src.RefreshTo(path); err == nil {
		t.Fatal("expected an error for a malformed refresh body")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("malformed refresh replaced the last-known-good store")
	}
}

func TestRefreshToOfflineSkipsNetwork(t *testing.T) {
	t.Setenv("SMIDJA_OFFLINE", "1")
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := (&CatalogSource{BaseURL: srv.URL}).RefreshTo(path); err != nil {
		t.Fatalf("RefreshTo offline: %v", err)
	}
	if hit {
		t.Error("offline refresh made a network request")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("offline refresh must not create the store file")
	}
}

func TestRefreshToOfflineWithCache(t *testing.T) {
	srv := newCatalogServer(t, readFixture(t)).start()
	path := filepath.Join(t.TempDir(), "models-store.json")
	src := &CatalogSource{BaseURL: srv.URL}
	if err := src.RefreshTo(path); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	t.Setenv("SMIDJA_OFFLINE", "1")
	if err := src.RefreshTo(path); err != nil {
		t.Fatalf("offline refresh with cache: %v", err)
	}
	store, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Providers) != 3 {
		t.Errorf("offline refresh lost the cache: %v", providerKeys(store))
	}
}

func TestRefreshToRequestError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	if err := (&CatalogSource{HTTP: client}).RefreshTo(path); err == nil {
		t.Fatal("expected an error for a failed request")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("failed refresh must not write a store")
	}
}

func TestRefreshToDefaultURL(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("ETag", `"x"`)
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	t.Setenv("SMIDJA_OFFLINE", "")
	orig := defaultCatalogURL
	t.Cleanup(func() { defaultCatalogURL = orig })
	defaultCatalogURL = srv.URL
	if err := (&CatalogSource{}).RefreshTo(filepath.Join(t.TempDir(), "m.json")); err != nil {
		t.Fatalf("RefreshTo: %v", err)
	}
	if gotURL != "/" {
		t.Errorf("requested %q, want the default base URL", gotURL)
	}
}

func TestLoadStoreMissingAndCorrupt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	store, err := LoadStore(missing)
	if err != nil {
		t.Fatalf("LoadStore missing: %v", err)
	}
	if len(store.Providers) != 0 {
		t.Errorf("missing store providers = %v", providerKeys(store))
	}
	corrupt := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(corrupt, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStore(corrupt); err == nil {
		t.Fatal("LoadStore accepted a corrupt file")
	}
}

func TestStoreFileModelsFlattenSorted(t *testing.T) {
	sf := StoreFile{Providers: map[string]ProviderState{
		"zzz": {Models: map[string]CatalogRecord{
			"m1": {ID: "m1", Provider: "zzz", ContextWindow: json.RawMessage(`100`)},
			"m0": {ID: "m0", Provider: "zzz", ContextWindow: json.RawMessage(`0`)},
		}},
		"aaa": {Models: map[string]CatalogRecord{
			"m2": {ID: "m2", Provider: "aaa", ContextWindow: json.RawMessage(`200`)},
		}},
	}}
	got := sf.Models()
	if len(got) != 2 {
		t.Fatalf("Models = %+v, want 2 (zero-window skipped)", got)
	}
	if got[0].ID != "aaa/m2" || got[1].ID != "zzz/m1" {
		t.Errorf("Models = %+v, want sorted ids", got)
	}
}

func TestMergeRefreshedPrecedence(t *testing.T) {
	r := NewRegistry()
	store := StoreFile{Providers: map[string]ProviderState{
		"anthropic": {Models: map[string]CatalogRecord{
			"claude-sonnet-4.5": {ID: "claude-sonnet-4.5", Provider: "anthropic", ContextWindow: json.RawMessage(`999000`)},
		}},
		"vendor": {Models: map[string]CatalogRecord{
			"new-model": {ID: "new-model", Provider: "vendor", ContextWindow: json.RawMessage(`12345`)},
		}},
	}}
	local := []ModelInfo{
		{ID: "anthropic/claude-sonnet-4.5", ContextWindow: 1_500_000, Provider: "anthropic"},
		{ID: "local/only", ContextWindow: 1, Provider: "local"},
	}
	r.MergeRefreshed(store, local)
	if m, ok := r.Get("anthropic/claude-sonnet-4.5"); !ok || m.ContextWindow != 1_500_000 {
		t.Errorf("local override lost: %+v ok=%v", m, ok)
	}
	if m, ok := r.Get("vendor/new-model"); !ok || m.ContextWindow != 12_345 {
		t.Errorf("store model lost: %+v ok=%v", m, ok)
	}
	if _, ok := r.Get("local/only"); !ok {
		t.Error("local-only model missing")
	}
	if _, ok := r.Get("openai/gpt-5"); !ok {
		t.Error("built-in fallback must survive a refresh merge")
	}
}

func TestLoadLocalOverridesUnifiedShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	body := `{"anthropic":{"claude-sonnet-4.5":{"id":"claude-sonnet-4.5","provider":"anthropic","contextWindow":777}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLocalOverrides(path)
	if err != nil {
		t.Fatalf("LoadLocalOverrides: %v", err)
	}
	if len(got) != 1 || got[0].ID != "anthropic/claude-sonnet-4.5" || got[0].ContextWindow != 777 {
		t.Errorf("got %+v", got)
	}
}

func TestLoadLocalOverridesArrayShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	body := `[{"id":"anthropic/claude-x","context_length":123},{"id":"","context_length":5}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLocalOverrides(path)
	if err != nil {
		t.Fatalf("LoadLocalOverrides: %v", err)
	}
	if len(got) != 1 || got[0].ID != "anthropic/claude-x" || got[0].ContextWindow != 123 {
		t.Errorf("got %+v", got)
	}
}

func TestLoadLocalOverridesMissingAndBad(t *testing.T) {
	got, err := LoadLocalOverrides(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || got != nil {
		t.Errorf("missing overrides = %v, %v; want nil, nil", got, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocalOverrides(bad); err == nil {
		t.Fatal("LoadLocalOverrides accepted garbage")
	}
}

func TestLocalOverridesPathPreference(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	if p := LocalOverridesPath(ws, home); p != "" {
		t.Errorf("found %q with no files present", p)
	}
	if err := os.MkdirAll(filepath.Join(home, ".smidja"), 0o755); err != nil {
		t.Fatal(err)
	}
	homeFile := filepath.Join(home, ".smidja", "models.json")
	if err := os.WriteFile(homeFile, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := LocalOverridesPath(ws, home); p != homeFile {
		t.Errorf("path = %q, want the home file", p)
	}
	wsFile := filepath.Join(ws, ".smidja", "models.json")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsFile, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := LocalOverridesPath(ws, home); p != wsFile {
		t.Errorf("path = %q, want the workspace file", p)
	}
}

func TestStorePathFor(t *testing.T) {
	if got := StorePathFor("/home/u/.smidja/sessions"); got != filepath.Join("/home/u/.smidja", modelsStoreName) {
		t.Errorf("StorePathFor = %q", got)
	}
}

func TestParseLocalOverridesEmpty(t *testing.T) {
	if got, err := parseLocalOverrides([]byte("  \n")); err != nil || got != nil {
		t.Errorf("empty overrides = %v, %v", got, err)
	}
}

func TestOfflineModeTruthiness(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"1", true},
		{"yes", true},
		{"ON", true},
	} {
		if got := envTruthy(tc.value); got != tc.want {
			t.Errorf("envTruthy(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestRefreshToUsesLastKnownGoodOnCorruptCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newCatalogServer(t, readFixture(t)).start()
	defer srv.Close()
	if err := (&CatalogSource{BaseURL: srv.URL}).RefreshTo(path); err != nil {
		t.Fatalf("RefreshTo with corrupt cache: %v", err)
	}
	store, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Providers) != 3 {
		t.Errorf("providers = %v, want fresh data replacing the corrupt cache", providerKeys(store))
	}
}

func providerKeys(sf StoreFile) []string {
	var out []string
	for pid := range sf.Providers {
		out = append(out, pid)
	}
	return out
}

func TestFetchNilClientUsesDefault(t *testing.T) {
	orig := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"p":{"m":{"id":"m","provider":"p","contextWindow":7}}}`), nil
	})}
	t.Cleanup(func() { http.DefaultClient = orig })
	got, err := Fetch(context.Background(), CatalogSource{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].ID != "p/m" {
		t.Errorf("got %+v", got)
	}
}

func TestCatalogRecordInfoLenientWindow(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
	}{
		{`200000`, 200_000},
		{`1048576.5`, 1_048_576},
		{`"163840"`, 163_840},
		{`null`, 0},
		{`true`, 0},
		{`"NaN"`, 0},
	} {
		rec := CatalogRecord{ID: "p/m", Provider: "p", ContextWindow: json.RawMessage(tc.raw)}
		if got := rec.Info().ContextWindow; got != tc.want {
			t.Errorf("window %s = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestParseLocalOverridesTolerantProviders(t *testing.T) {
	body := `{"ok":{"m":{"id":"m","provider":"ok","contextWindow":5}},"bad":[1]}`
	got, err := parseLocalOverrides([]byte(body))
	if err != nil {
		t.Fatalf("parseLocalOverrides: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ok/m" {
		t.Errorf("got %+v", got)
	}
}

func TestRefreshToWritesParentDir(t *testing.T) {
	srv := newCatalogServer(t, `{}`).start()
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "deep", "nested", "models-store.json")
	if err := (&CatalogSource{BaseURL: srv.URL}).RefreshTo(path); err != nil {
		t.Fatalf("RefreshTo: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("store not written at %s: %v", path, err)
	}
}

func TestFetchRejectsBadURL(t *testing.T) {
	if _, err := Fetch(context.Background(), CatalogSource{BaseURL: "://bad-url"}); err == nil {
		t.Fatal("Fetch accepted an unbuildable URL")
	}
}

func TestLoadStoreEmptyObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if store.Providers == nil || len(store.Providers) != 0 {
		t.Errorf("Providers = %v, want an empty non-nil map", store.Providers)
	}
}

func TestRefreshToWriteParentIsFile(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "models-store.json")
	srv := newCatalogServer(t, `{}`).start()
	defer srv.Close()
	if err := (&CatalogSource{BaseURL: srv.URL}).RefreshTo(path); err == nil {
		t.Fatal("RefreshTo succeeded where the parent cannot be created")
	}
}

func TestProviderStateZeroValueMarshals(t *testing.T) {
	sf := StoreFile{Providers: map[string]ProviderState{"p": {Models: map[string]CatalogRecord{
		"m": {ID: "m", Name: "M"},
	}}}}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["providers"]; !ok {
		t.Errorf("marshal = %s", data)
	}
}
