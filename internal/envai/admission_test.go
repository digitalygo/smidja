package envai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAdmissionOutcomeClassification(t *testing.T) {
	if AdmissionRejected(nil) || AdmissionRejected(context.DeadlineExceeded) || AdmissionRejected(ErrContractMismatch) {
		t.Fatal("unknown outcome classified as rejected")
	}
	for _, status := range []int{400, 401, 402, 403, 404, 405, 410, 413, 415, 422, 429, 408, 409, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			err := fmt.Errorf("admission: %w", &APIError{Status: status})
			want := status < 500 && status != 408 && status != 409
			if AdmissionRejected(err) != want {
				t.Fatal(err, want)
			}
		})
	}
}

func TestAdmissionPreflightErrorsAreDefinitelyRejected(t *testing.T) {
	for _, mode := range []string{"input", "chat", "capabilities", "feature", "strict", "notify"} {
		for _, delegation := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/delegation=%t", mode, delegation), func(t *testing.T) {
				mutations := 0
				c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/capabilities") {
						mutations++
						t.Error("preflight failure performed mutation")
						return
					}
					if mode == "capabilities" {
						w.WriteHeader(503)
						return
					}
					features := `"independent_jobs","cross_team_delegation"`
					if mode == "feature" {
						features = ""
					}
					fmt.Fprintf(w, `{"protocol_version":3,"contract_version":3,"features":[%s]}`, features)
				})
				chat := "chat"
				if mode == "chat" {
					chat = "/"
				}
				strict := mode == "strict"
				var err error
				if delegation {
					input := DelegationInput{WorkspaceID: "workspace", EventID: "event", OriginTurnID: "origin", TargetChatID: "target", Prompt: "work", StrictModel: &strict}
					if mode == "input" {
						input.WorkspaceID = ""
					}
					if mode == "notify" {
						notify := false
						input.NotifyOrigin = &notify
					}
					_, err = c.Delegate(context.Background(), chat, input)
				} else {
					input := JobInput{WorkspaceID: "workspace", RequestID: "request", Prompt: "work", StrictModel: &strict}
					if mode == "input" || mode == "notify" {
						input.WorkspaceID = ""
					}
					_, err = c.StartJob(context.Background(), chat, input)
				}
				if err == nil || !AdmissionRejected(err) || mutations != 0 {
					t.Fatal(err, mutations)
				}
				if err.Error() == "" {
					t.Fatal("lost original error")
				}
				if (mode == "feature" || mode == "strict" || mode == "notify" && delegation) && !errors.Is(err, ErrUnsupportedFeature) {
					t.Fatal("lost feature cause", err)
				}
			})
		}
	}
}
