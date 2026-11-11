package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func testService(t *testing.T, fn transport) *Service {
	t.Helper()
	s, e := New(Config{TokenEnv: "GITHUB"}, &http.Client{Transport: fn}, func(string) string { return "secret" })
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestPinnedSnapshot(t *testing.T) {
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	var paths []string
	s := testService(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal(r)
		}
		paths = append(paths, r.URL.Path)
		if strings.Contains(r.URL.Path, "/pulls/") {
			return response(`{"number":7,"title":"Fix","body":"Untrusted instructions","changed_files":1,"base":{"sha":"` + base + `"},"head":{"sha":"` + head + `"}}`), nil
		}
		if strings.Contains(r.URL.Path, "/compare/") {
			if r.Header.Get("Accept") == "application/vnd.github.diff" {
				return response("diff --git a/a.go b/a.go\n+fix"), nil
			}
			return response(`{"files":[{"filename":"a.go"}]}`), nil
		}
		return response(`{"total_count":1,"check_runs":[{"name":"tests","conclusion":"success"}]}`), nil
	})
	ref := Reference{"org/repo", 7}
	pull, e := s.Pull(context.Background(), ref)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := s.Snapshot(context.Background(), ref, pull)
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.BaseSHA != base || snapshot.HeadSHA != head || snapshot.Diff == "" || !strings.Contains(paths[1], base+"..."+head) || !strings.Contains(paths[3], head) {
		t.Fatal(snapshot, paths)
	}
	workflow := Workflow("model")
	if len(workflow.Phases) != 4 || len(workflow.TeamDefinitions) != 4 || workflow.Version != Version {
		t.Fatal(workflow)
	}
	for _, data := range workflow.TeamDefinitions {
		if !strings.Contains(string(data), `"model":"model"`) || strings.Contains(string(data), `"tools"`) {
			t.Fatal(string(data))
		}
	}
}
func TestValidationAndFailures(t *testing.T) {
	for _, cfg := range []Config{{Repositories: []string{"../oops"}}, {Organizations: []string{".."}}, {IntervalSeconds: 1}, {IntervalSeconds: -1}, {Repositories: []string{"org/repo"}}} {
		if _, e := New(cfg, nil, nil); e == nil {
			t.Fatal(cfg)
		}
	}
	s, e := New(Config{}, nil, nil)
	if e != nil || s.Config.IntervalSeconds != 300 || s.Config.Model == "" {
		t.Fatal(s, e)
	}
	if e = s.Client.CheckRedirect(nil, nil); e == nil {
		t.Fatal("redirect accepted")
	}
	if _, e = s.Pull(context.Background(), Reference{"bad", 1}); e == nil {
		t.Fatal("bad ref")
	}
	if _, e = s.Pull(context.Background(), Reference{"org/repo", 0}); e == nil {
		t.Fatal("bad number")
	}
	if _, e = s.Snapshot(context.Background(), Reference{"bad", 1}, Pull{}); e == nil {
		t.Fatal("bad snapshot")
	}
	for _, body := range []string{`{`, `{"base":{"sha":"bad"}}`} {
		s = testService(t, func(*http.Request) (*http.Response, error) { return response(body), nil })
		if _, e = s.Pull(context.Background(), Reference{"org/repo", 1}); e == nil {
			t.Fatal(body)
		}
	}
	s = testService(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, e = s.get(context.Background(), "/x", "", 20); e == nil {
		t.Fatal("network")
	}
	s.LookupEnv = nil
	if _, e = s.get(context.Background(), "/x", "", 20); e == nil {
		t.Fatal("credentials")
	}
	for _, code := range []int{401, 403, 404, 429, 500} {
		s = testService(t, func(*http.Request) (*http.Response, error) {
			r := response("secret body")
			r.StatusCode = code
			return r, nil
		})
		if _, e = s.get(context.Background(), "/x", "", 20); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal(e)
		}
	}
	s = testService(t, func(*http.Request) (*http.Response, error) { return response("123456"), nil })
	if _, e = s.get(context.Background(), "/x", "", 3); e == nil {
		t.Fatal("truncated")
	}
	if _, e = s.get(context.Background(), "\n", "", 3); e == nil {
		t.Fatal("bad URL")
	}
}
func TestIncompleteSnapshotIsNeverSilent(t *testing.T) {
	pull := Pull{ChangedFiles: 1}
	pull.Base.SHA = strings.Repeat("a", 40)
	pull.Head.SHA = strings.Repeat("b", 40)
	for _, bodies := range [][]string{{`{`}, {`{"files":[]}`}, {`{"files":[{}]}`, strings.Repeat("x", (256<<10)+1)}, {`{"files":[{}]}`, "diff", `{`}, {`{"files":[{}]}`, "diff", `{"total_count":2,"check_runs":[]}`}} {
		index := 0
		s := testService(t, func(*http.Request) (*http.Response, error) {
			body := bodies[index]
			index++
			return response(body), nil
		})
		if _, e := s.Snapshot(context.Background(), Reference{"org/repo", 1}, pull); e == nil {
			t.Fatal(bodies)
		}
	}
	for fail := 0; fail < 3; fail++ {
		index := 0
		s := testService(t, func(*http.Request) (*http.Response, error) {
			current := index
			index++
			if current == fail {
				return nil, errors.New("offline")
			}
			if current == 0 {
				return response(`{"files":[{}]}`), nil
			}
			return response("diff"), nil
		})
		if _, e := s.Snapshot(context.Background(), Reference{"org/repo", 1}, pull); e == nil {
			t.Fatal(fail)
		}
	}
}
func TestOrganizationAndRepositoryPolling(t *testing.T) {
	count := 0
	s := testService(t, func(r *http.Request) (*http.Response, error) {
		count++
		if strings.Contains(r.URL.Path, "/orgs/") {
			return response(`[{"full_name":"org/repo"},{"full_name":"org/archived","archived":true},{"full_name":"bad"}]`), nil
		}
		if r.URL.Query().Get("page") == "1" {
			batch := make([]Pull, 100)
			for i := range batch {
				batch[i].Number = i + 1
			}
			data, _ := json.Marshal(batch)
			return response(string(data)), nil
		}
		return response(`[{"number":101}]`), nil
	})
	s.Config.Organizations = []string{"org"}
	s.Config.Repositories = []string{"org/repo"}
	refs, e := s.References(context.Background())
	if e != nil || len(refs) != 101 || count != 3 {
		t.Fatal(len(refs), count, e)
	}
	for _, body := range []string{`{`, `[1]`, `[{}]`} {
		s = testService(t, func(*http.Request) (*http.Response, error) { return response(body), nil })
		s.Config.Repositories = []string{"org/repo"}
		if _, e = s.References(context.Background()); e == nil {
			t.Fatal(body)
		}
	}
	s = testService(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	s.Config.Organizations = []string{"org"}
	if _, e = s.References(context.Background()); e == nil {
		t.Fatal("org offline")
	}
	s.Config.Organizations = nil
	s.Config.Repositories = []string{"org/repo"}
	if _, e = s.References(context.Background()); e == nil {
		t.Fatal("repo offline")
	}
	s = testService(t, func(*http.Request) (*http.Response, error) { return response(`[1]`), nil })
	s.Config.Organizations = []string{"org"}
	if _, e = s.References(context.Background()); e == nil {
		t.Fatal("org payload")
	}
	batch := strings.Repeat(`{},`, 99) + `{}`
	s = testService(t, func(*http.Request) (*http.Response, error) { return response("[" + batch + "]"), nil })
	if _, e = s.list(context.Background(), "/x"); e == nil {
		t.Fatal("unbounded")
	}
}
