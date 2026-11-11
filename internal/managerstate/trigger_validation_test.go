package managerstate

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/verk"
)

func TestGraphTriggerRunnableValidation(t *testing.T) {
	cases := []struct {
		name    string
		trigger verk.Trigger
		message string
	}{
		{"cron", verk.Trigger{Type: "schedule", Schedule: "every morning", Timezone: "UTC"}, "five numeric cron fields"},
		{"timezone", verk.Trigger{Type: "schedule", Schedule: "0 9 * * *", Timezone: "Invalid/Zone"}, "invalid schedule timezone"},
		{"scope", verk.Trigger{Type: "push", Scope: "all", Repositories: []string{"studio/repo"}}, "unknown repository scope"},
		{"repository", verk.Trigger{Type: "push", Scope: "specific", Repositories: []string{"studio/*"}}, "invalid repository"},
		{"action", verk.Trigger{Type: "pr", Scope: "specific", Repositories: []string{"studio/repo"}}, "explicit actions"},
		{"source", verk.Trigger{Type: "webhook", SourceRef: "github"}, "explicit custom source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workflow := graphWorkflow()
			workflow.Graph.Nodes[0].Triggers = []verk.Trigger{tc.trigger}
			if err := ValidateWorkflowDraft(workflow); err != nil {
				t.Fatal("editable draft rejected", err)
			}
			err := ValidateWorkflow(workflow)
			if err == nil || !strings.Contains(err.Error(), "node entry trigger 1") || !strings.Contains(err.Error(), tc.message) {
				t.Fatal("missing actionable trigger validation", err)
			}
			store, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err = store.CreateTask(StartTask{Workflow: workflow}); err == nil || len(store.ListTasks()) != 0 {
				t.Fatal("invalid trigger started durable task", err)
			}
		})
	}
	for _, trigger := range []verk.Trigger{{Type: "schedule", Schedule: "0 9 * * 1-5", Timezone: "Europe/Rome"}, {Type: "push", Scope: "specific", Repositories: []string{"studio/repo"}}, {Type: "webhook", SourceRef: "catalog:updated"}} {
		workflow := graphWorkflow()
		workflow.Graph.Nodes[0].Triggers = []verk.Trigger{trigger}
		if err := ValidateWorkflow(workflow); err != nil {
			t.Fatal("valid trigger rejected", err)
		}
	}
}
