package manager

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/managerstate"
)

func TestEmbeddedUI(t *testing.T) {
	store, err := managerstate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := New(Config{Token: strings.Repeat("a", 32), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Body.String() != indexHTML {
		t.Fatalf("index response: %d %v", response.Code, response.Header())
	}
	for _, fragment := range []string{"id=\"login\"", "id=\"chat\"", "id=\"tasks\"", "id=\"files\"", "id=\"devices\"", "id=\"workflows\"", "id=\"mainNav\"", "id=\"explorerPane\"", "id=\"diffPane\"", "/assets/ink.css", "/assets/verk.js", "/assets/verk_forms.js", "/assets/verk_forms.css", "/assets/team_editor.js"} {
		if !strings.Contains(indexHTML, fragment) {
			t.Errorf("missing UI contract %q", fragment)
		}
	}
	entries, err := webAssets.ReadDir("web")
	if err != nil {
		t.Fatal(err)
	}
	var scripts strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "index.html" {
			continue
		}
		response = httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("GET", "/assets/"+entry.Name(), nil))
		expected, _ := webAssets.ReadFile("web/" + entry.Name())
		if response.Code != 200 || response.Body.String() != string(expected) {
			t.Fatalf("asset %s: %d", entry.Name(), response.Code)
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("asset missing security headers")
		}
		if strings.HasSuffix(entry.Name(), ".js") {
			scripts.Write(expected)
			if response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
				t.Fatal("incorrect script MIME")
			}
		} else if response.Header().Get("Content-Type") != "text/css; charset=utf-8" {
			t.Fatal("incorrect style MIME")
		}
	}
	for _, fragment := range []string{"expected_sha256:file.sha256", "credentials:'same-origin'", "options.headers['X-CSRF']=state.csrf", "e.textContent=text", "retry-classification", "sessionGeneration", "fileGeneration", "[data-busy]", "beforeunload"} {
		if !strings.Contains(scripts.String(), fragment) {
			t.Errorf("missing behavior contract %q", fragment)
		}
	}
	for _, unsafe := range []string{"innerHTML", "localStorage", "sessionStorage", "eval("} {
		if strings.Contains(scripts.String(), unsafe) {
			t.Errorf("unexpected unsafe UI operation %q", unsafe)
		}
	}
	for _, target := range []string{"/missing", "/assets/missing.js", "/assets/index.html", "/assets/nested/core.js"} {
		response = httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		if response.Code != 404 {
			t.Errorf("%s status %d", target, response.Code)
		}
	}
	response = httptest.NewRecorder()
	server.asset(response, httptest.NewRequest("GET", "/assets/../ui.go", nil))
	if response.Code != 404 {
		t.Fatal("asset escaped directory")
	}
}
