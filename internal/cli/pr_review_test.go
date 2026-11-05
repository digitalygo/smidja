package cli

import (
	"github.com/digitalygo/smidja/internal/prreview"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagerPRReviewConfiguration(t *testing.T) {
	cfg := validManagerConfig(t)
	cfg.PRReview = &prreview.Config{TokenEnv: "GITHUB"}
	server, store, err := buildManager(cfg, serviceDeps())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	defer server.Close()
	r := httptest.NewRequest("GET", "/api/pr-review/status", nil)
	r.Header.Set("Authorization", "Bearer "+serviceDeps().Env("TOKEN"))
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":true`) {
		t.Fatal(w.Code, w.Body)
	}
	cfg.PRReview = &prreview.Config{Repositories: []string{"invalid"}}
	if other, st, err := buildManager(cfg, serviceDeps()); err == nil {
		other.Close()
		st.Close()
		t.Fatal("invalid review config accepted")
	}
}
