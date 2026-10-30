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
	if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("response: %d %v", response.Code, response.Header())
	}
	if response.Body.String() != indexHTML {
		t.Fatal("embedded UI changed during serving")
	}
	for _, fragment := range []string{"id=\"login\"", "id=\"chat\"", "id=\"tasks\"", "id=\"files\"", "id=\"devices\"", "expected_sha256:file.sha256", "credentials:'same-origin'", "options.headers['X-CSRF']=state.csrf", "e.textContent=text", "retry-classification", "sessionGeneration", "fileGeneration", "[data-busy]", "beforeunload", "@media(max-width:700px)"} {
		if !strings.Contains(indexHTML, fragment) {
			t.Errorf("missing UI contract %q", fragment)
		}
	}
	for _, unsafe := range []string{"innerHTML", "localStorage", "sessionStorage", "https://", "http://", "eval("} {
		if strings.Contains(indexHTML, unsafe) {
			t.Errorf("unexpected unsafe or external UI dependency %q", unsafe)
		}
	}
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/missing", nil))
	if response.Code != 404 {
		t.Fatalf("unknown route status %d", response.Code)
	}
}
