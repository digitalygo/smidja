package verktriggers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/verk"
)

const pushBody = `{"ref":"refs/heads/main","before":"abc","after":"def","repository":{"full_name":"Studio/Repo","owner":{"login":"studio"}}}`

func sign(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
func githubHeaders(event string) http.Header {
	return http.Header{"X-Github-Event": {event}, "X-Github-Delivery": {"delivery-1"}, "X-Hub-Signature-256": {sign(pushBody, "secret")}}
}
func TestAuthentication(t *testing.T) {
	headers := githubHeaders("push")
	source, delivery, err := Authenticate(headers, []byte(pushBody), "secret")
	if err != nil || source != "github" || delivery != "delivery-1" {
		t.Fatal(source, delivery, err)
	}
	for _, mutation := range []func(http.Header){func(h http.Header) { h.Set("X-Hub-Signature-256", "bad") }, func(h http.Header) { h.Set("X-Hub-Signature-256", "sha256=zz") }, func(h http.Header) { h.Set("X-Hub-Signature-256", "sha256=01") }, func(h http.Header) { h.Set("X-GitHub-Delivery", "") }, func(h http.Header) { h.Set("X-Smidja-Source", "source") }} {
		h := headers.Clone()
		mutation(h)
		if _, _, err := Authenticate(h, []byte(pushBody), "secret"); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
	}
	for _, secret := range []string{"", "wrong"} {
		if _, _, err := Authenticate(headers, []byte(pushBody), secret); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
	if _, _, err := Authenticate(headers, []byte(pushBody+" "), "secret"); err == nil {
		t.Fatal("accepted changed payload")
	}
	custom := http.Header{}
	custom.Set("X-Smidja-Source", "catalog:updated")
	custom.Set("X-Smidja-Delivery", "delivery-2")
	custom.Set("X-Smidja-Signature-256", sign("catalog:updated\ndelivery-2\n{}", "secret"))
	if source, _, err := Authenticate(custom, []byte("{}"), "secret"); err != nil || source != "catalog:updated" {
		t.Fatal(source, err)
	}
	custom.Set("X-Smidja-Delivery", "different")
	if _, _, err := Authenticate(custom, []byte("{}"), "secret"); err == nil {
		t.Fatal("unsigned metadata accepted")
	}
}
func TestNormalizeAndScope(t *testing.T) {
	event, err := Decode(githubHeaders("push"), []byte(pushBody), "github", "delivery-1")
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "push" || event.Repository != "studio/repo" || event.Branch != "main" || event.SHA != "def" || event.PayloadHash == "" {
		t.Fatal(event)
	}
	trigger := verk.Trigger{Type: "push", Scope: "specific", Repositories: []string{"Studio/Repo"}, Branches: []string{"main"}}
	if !Match(trigger, event) {
		t.Fatal("authorized push rejected")
	}
	trigger.Repositories = []string{"studio/other"}
	if Match(trigger, event) {
		t.Fatal("foreign repo accepted")
	}
	trigger.Scope = "organization"
	trigger.Repositories = []string{"studio"}
	if !Match(trigger, event) {
		t.Fatal("authorized organization rejected")
	}
	event.Repository = "studio-other/repo"
	if Match(trigger, event) {
		t.Fatal("organization prefix accepted")
	}
	event.Repository = "studio/repo"
	event.Branch = "other"
	if Match(trigger, event) {
		t.Fatal("foreign branch accepted")
	}
	trigger.Branches = nil
	trigger.Events = []string{"other"}
	if Match(trigger, event) {
		t.Fatal("foreign action accepted")
	}
	trigger.Events = nil
	event.Source = "custom"
	if Match(trigger, event) {
		t.Fatal("custom source impersonated GitHub")
	}
	custom := verk.Trigger{Type: "webhook", SourceRef: "custom"}
	event.Type = "webhook"
	if !Match(custom, event) {
		t.Fatal("custom match missing")
	}
	custom.SourceRef = "other"
	if Match(custom, event) {
		t.Fatal("foreign source accepted")
	}
	pr := `{"action":"synchronize","repository":{"full_name":"studio/repo"},"pull_request":{"number":2,"head":{"sha":"head"},"base":{"ref":"main"}}}`
	event, err = Decode(githubHeaders("pull_request"), []byte(pr), "github", "pr-1")
	if err != nil || event.Type != "pr" || event.Action != "synchronize" || event.Branch != "main" {
		t.Fatal(event, err)
	}
	for _, kind := range []string{"workflow_run", "check_run", "check_suite"} {
		payload := `{"action":"completed","repository":{"full_name":"studio/repo"},"` + kind + `":{"id":12,"status":"completed","conclusion":"success","head_sha":"sha","check_suite":{"head_branch":"main"}}}`
		event, err = Decode(githubHeaders(kind), []byte(payload), "github", "ci-1")
		if err != nil || event.Type != "ci" || event.Action != "success" || event.Branch != "main" {
			t.Fatal(event, err)
		}
	}
	event, err = Decode(http.Header{}, []byte(`{"action":123,"data":[1,true]}`), "custom", "id")
	if err != nil || event.Type != "webhook" {
		t.Fatal(event, err)
	}
}
func TestRejectForgedAndIncompleteEvents(t *testing.T) {
	for _, tc := range []struct{ kind, body string }{
		{"push", "{"}, {"push", "null"}, {"push", "[]"}, {"push", pushBody + " {}"}, {"push", "{}"}, {"pull_request", pushBody}, {"check_run", pushBody},
		{"push", strings.Replace(pushBody, `"before":"abc",`, "", 1)}, {"push", strings.Replace(pushBody, `"login":"studio"`, `"login":"other"`, 1)},
		{"push", strings.Replace(pushBody, `"ref":"refs/heads/main"`, `"ref":123`, 1)},
		{"push", strings.TrimSuffix(pushBody, "}") + `,"pull_request":{}}`},
		{"pull_request", `{"action":"opened","repository":{"full_name":"a/b"},"pull_request":{}}`},
		{"workflow_run", `{"action":"completed","repository":{"full_name":"a/b"},"workflow_run":{}}`},
	} {
		if _, err := Decode(githubHeaders(tc.kind), []byte(tc.body), "github", "id"); err == nil {
			t.Fatalf("accepted %s %s", tc.kind, tc.body)
		}
	}
	for _, tc := range []struct{ kind, body string }{{"push", strings.Replace(pushBody, "refs/heads/main", "refs/tags/v1", 1)}, {"push", strings.TrimSuffix(pushBody, "}") + `,"deleted":true}`}, {"unknown", pushBody}, {"workflow_run", `{"action":"requested","repository":{"full_name":"a/b"},"workflow_run":{"id":1}}`}} {
		if _, err := Decode(githubHeaders(tc.kind), []byte(tc.body), "github", "id"); !errors.Is(err, ErrUnsupported) {
			t.Fatal(err)
		}
	}
}
func TestTriggerValidation(t *testing.T) {
	for _, trigger := range []verk.Trigger{{Type: "smidr"}, {Type: "schedule", Schedule: "*/5 * * * *", Timezone: "UTC"}, {Type: "webhook", SourceRef: "source"}, {Type: "pr", Scope: "list", Repositories: []string{"a/b", "a/c"}, Events: []string{"opened"}}, {Type: "ci", Scope: "organization", Repositories: []string{"studio"}, Events: []string{"failure"}}} {
		if err := Validate(trigger); err != nil {
			t.Fatal(trigger, err)
		}
	}
	for _, trigger := range []verk.Trigger{{Type: "unknown"}, {Type: "webhook"}, {Type: "webhook", SourceRef: "github"}, {Type: "push"}, {Type: "push", Scope: "all", Repositories: []string{"a/b"}}, {Type: "push", Scope: "specific", Repositories: []string{"a/b", "a/c"}}, {Type: "push", Scope: "list", Repositories: []string{"a/*"}}, {Type: "push", Scope: "organization", Repositories: []string{"a/b"}}, {Type: "push", Scope: "specific", Repositories: []string{"a/b"}, Branches: []string{"main*"}}, {Type: "pr", Scope: "specific", Repositories: []string{"a/b"}}, {Type: "ci", Scope: "specific", Repositories: []string{"a/b"}, Events: []string{""}}} {
		if Validate(trigger) == nil || Match(trigger, Event{Type: trigger.Type}) {
			t.Fatal("accepted invalid trigger", trigger)
		}
	}
	if Key("a", "bc") == Key("ab", "c") || Key("a") != Key("a") {
		t.Fatal("unstable or ambiguous key")
	}
}
func TestSchedule(t *testing.T) {
	cases := []struct {
		expression, zone, at string
		due                  bool
	}{
		{"0 9 * * 1-5", "Europe/Rome", "2026-11-05T08:00:00Z", true}, {"0 9 * * 1-5", "Europe/Rome", "2026-11-07T08:00:00Z", false},
		{"*/15 8-10 * * *", "UTC", "2026-11-05T09:30:00Z", true}, {"*/15 8-10 * * *", "UTC", "2026-11-05T09:31:00Z", false},
		{"5/10 * * * *", "UTC", "2026-11-05T09:25:00Z", true}, {"0 0 1 * 7", "UTC", "2026-11-08T00:00:00Z", true}, {"0 0 1 * 7", "UTC", "2026-12-01T00:00:00Z", true},
		{"0,30 9 * 10 *", "UTC", "2026-11-05T09:30:00Z", false}, {"0,30 9 * 11 *", "UTC", "2026-11-05T09:30:00Z", true},
	}
	for _, tc := range cases {
		s, err := ParseSchedule(tc.expression, tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		now, _ := time.Parse(time.RFC3339, tc.at)
		slot, due := s.Due(now)
		if due != tc.due || due && slot == "" {
			t.Fatal(tc, slot, due)
		}
	}
	for _, expr := range []string{"* * * *", "* * * * * *", "61 * * * *", "* 4-2 * * *", "* x * * *", "*/0 * * * *", "*/61 * * * *", "*/x * * * *", "1/2/3 * * * *", "1-2-3 * * * *", "1-x * * * *"} {
		if _, err := ParseSchedule(expr, "UTC"); err == nil {
			t.Fatal(expr)
		}
	}
	for _, zone := range []string{"", "Local", "Not/AZone"} {
		if _, err := ParseSchedule("* * * * *", zone); err == nil {
			t.Fatal(zone)
		}
	}
	if _, due := (Schedule{}).Due(time.Now()); due {
		t.Fatal("zero schedule fired")
	}
	schedule, _ := ParseSchedule("30 2 * * *", "Europe/Rome")
	first, _ := time.Parse(time.RFC3339, "2026-10-25T00:30:00Z")
	second := first.Add(time.Hour)
	a, adue := schedule.Due(first)
	b, bdue := schedule.Due(second)
	if !adue || !bdue || a != b {
		t.Fatal("DST duplicated local slot identity", a, b)
	}
}

func TestCronWildcardInListRetainsDaySemantics(t *testing.T) {
	schedule, err := ParseSchedule("0 0 1,* * 1", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	sunday, _ := time.Parse(time.RFC3339, "2026-11-08T00:00:00Z")
	if _, due := schedule.Due(sunday); due {
		t.Fatal("weekday filter ignored")
	}
	if _, due := schedule.Due(sunday.Add(24 * time.Hour)); !due {
		t.Fatal("Monday not selected")
	}
}
func TestCustomSourceCannotImpersonateGitHub(t *testing.T) {
	for _, source := range []string{"github", "github:push"} {
		h := http.Header{}
		h.Set("X-Smidja-Source", source)
		h.Set("X-Smidja-Delivery", "id")
		h.Set("X-Smidja-Signature-256", sign(source+"\nid\n{}", "secret"))
		if _, _, err := Authenticate(h, []byte("{}"), "secret"); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
	}
}
