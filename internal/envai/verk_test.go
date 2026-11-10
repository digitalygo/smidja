package envai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func verkFixture(t *testing.T, features []string, response string, status int, check func(*http.Request)) (*Client, *int) {
	t.Helper()
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/atp/v3/capabilities" {
			json.NewEncoder(w).Encode(map[string]any{"protocol_version": 3, "contract_version": 3, "features": features})
			return
		}
		calls++
		if check != nil {
			check(r)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, response)
	})
	return c, &calls
}

func verkChoiceInput() ChoiceInput {
	return ChoiceInput{RequestID: "choice_1", Context: json.RawMessage(`{"full_output":"line one\nline two"}`), Question: "Choose branch", Choices: []Choice{{Label: "review", Description: "Needs review"}, {Label: "ready", Description: "Ready"}}}
}

func TestVerkIndependentJobContract(t *testing.T) {
	strict := true
	input := JobInput{WorkspaceID: "ws", RequestID: "job_1", Prompt: "complete automatic workflow", StrictModel: &strict}
	c, calls := verkFixture(t, []string{"independent_jobs", "strict_model"}, `{"task_id":"task","turn_id":"initial"}`, 202, func(r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/atp/v3/chats/execution/jobs" {
			t.Error(r.Method, r.URL.Path)
		}
		var got JobInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if got.WorkspaceID != input.WorkspaceID || got.RequestID != input.RequestID || got.Prompt != input.Prompt || got.StrictModel == nil || !*got.StrictModel {
			t.Error(got)
		}
	})
	got, err := c.StartJob(context.Background(), "execution", input)
	if err != nil || got.TaskID != "task" || got.TurnID != "initial" || *calls != 1 {
		t.Fatal(got, err, *calls)
	}
}

func TestVerkMutationFeatureGates(t *testing.T) {
	strict := true
	tests := []struct {
		name     string
		features []string
		call     func(*Client) error
	}{
		{"job missing", nil, func(c *Client) error {
			_, e := c.StartJob(context.Background(), "chat", JobInput{WorkspaceID: "ws", RequestID: "id", Prompt: "work"})
			return e
		}},
		{"job strict missing", []string{"independent_jobs"}, func(c *Client) error {
			_, e := c.StartJob(context.Background(), "chat", JobInput{WorkspaceID: "ws", RequestID: "id", Prompt: "work", StrictModel: &strict})
			return e
		}},
		{"choice missing", nil, func(c *Client) error {
			_, e := c.ClassifyChoice(context.Background(), "chat", verkChoiceInput())
			return e
		}},
		{"originless missing", []string{"native_deliveries"}, func(c *Client) error {
			_, e := c.Deliver(context.Background(), "chat", DeliveryInput{WorkspaceID: "ws", EventID: "event", TaskID: "task", Status: "completed"})
			return e
		}},
		{"delivery strict missing", []string{"native_deliveries", "originless_deliveries"}, func(c *Client) error {
			_, e := c.Deliver(context.Background(), "chat", DeliveryInput{WorkspaceID: "ws", EventID: "event", TaskID: "task", Status: "completed", StrictModel: &strict})
			return e
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, calls := verkFixture(t, test.features, `{}`, 202, nil)
			err := test.call(c)
			if !errors.Is(err, ErrUnsupportedFeature) || *calls != 0 {
				t.Fatal(err, *calls)
			}
		})
	}
}

func TestVerkChoiceClosedResponseAndWire(t *testing.T) {
	input := verkChoiceInput()
	for _, test := range []struct {
		name, response string
		valid          bool
	}{
		{"valid", `{"choice":"ready","model":"luna","request_id":"choice_1","invocation_id":"inv"}`, true},
		{"invented", `{"choice":"invented","model":"luna","request_id":"choice_1","invocation_id":"inv"}`, false},
		{"wrong request", `{"choice":"ready","model":"luna","request_id":"other","invocation_id":"inv"}`, false},
		{"missing model", `{"choice":"ready","request_id":"choice_1","invocation_id":"inv"}`, false},
		{"missing invocation", `{"choice":"ready","model":"luna","request_id":"choice_1"}`, false},
		{"invalid JSON", `{`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, calls := verkFixture(t, []string{"finite_choice_classification"}, test.response, 200, func(r *http.Request) {
				if r.URL.Path != "/api/v1/atp/v3/chats/execution/classify-choice" || r.Method != "POST" {
					t.Error(r.Method, r.URL.Path)
				}
				var got ChoiceInput
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if string(got.Context) != string(input.Context) || got.RequestID != input.RequestID || got.Question != input.Question || len(got.Choices) != 2 || got.Choices[0] != input.Choices[0] {
					t.Error(got)
				}
			})
			result, err := c.ClassifyChoice(context.Background(), "execution", input)
			if (err == nil) != test.valid || *calls != 1 {
				t.Fatal(result, err, *calls)
			}
		})
	}
}

func TestVerkUncertainMutationsNeverRetry(t *testing.T) {
	for _, route := range []string{"jobs", "classify-choice"} {
		for _, status := range []int{202, 409, 422, 502, 503} {
			t.Run(fmt.Sprintf("%s/%d", route, status), func(t *testing.T) {
				c, calls := verkFixture(t, []string{"independent_jobs", "finite_choice_classification"}, `{}`, status, nil)
				var err error
				if route == "jobs" {
					_, err = c.StartJob(context.Background(), "chat", JobInput{WorkspaceID: "ws", RequestID: "id", Prompt: "work"})
				} else {
					_, err = c.ClassifyChoice(context.Background(), "chat", verkChoiceInput())
				}
				if err == nil || *calls != 1 {
					t.Fatal(err, *calls)
				}
				if status == 202 && !strings.Contains(err.Error(), "uncertain") {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestVerkDeliveryOriginAndStrictPolicy(t *testing.T) {
	for _, origin := range []string{"", "original_turn"} {
		t.Run("origin="+origin, func(t *testing.T) {
			strict := true
			features := []string{"native_deliveries", "strict_model"}
			if origin == "" {
				features = append(features, "originless_deliveries")
			}
			c, calls := verkFixture(t, features, `{"event_id":"event","status":"queued","duplicate":false}`, 202, func(r *http.Request) {
				var got DeliveryInput
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if got.OriginTurnID != origin || got.StrictModel == nil || !*got.StrictModel || got.Output != "complete\noutput" {
					t.Error(got)
				}
			})
			receipt, err := c.Deliver(context.Background(), "notification", DeliveryInput{WorkspaceID: "ws", EventID: "event", OriginTurnID: origin, TaskID: "external", Status: "completed", Output: "complete\noutput", StrictModel: &strict})
			if err != nil || receipt.Status != "queued" || *calls != 1 {
				t.Fatal(receipt, err, *calls)
			}
		})
	}
}

func TestVerkIndependentTaskHasNoOrigin(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			features := []string{"detached_background"}
			if supported {
				features = append(features, "independent_jobs")
			}
			c, calls := verkFixture(t, features, `{"task_id":"task","origin_turn_id":"","status":"completed","output":"full final synthesis","result_turn_id":"final"}`, 200, nil)
			task, err := c.Task(context.Background(), "execution", "task")
			if (err == nil) != supported || *calls != 1 {
				t.Fatal(task, err, *calls)
			}
			if supported && (task.ResultTurnID != "final" || task.Output != "full final synthesis") {
				t.Fatal(task)
			}
		})
	}
}

func TestVerkRejectsInvalidInputsBeforeNetwork(t *testing.T) {
	c, calls := verkFixture(t, nil, `{}`, 200, func(r *http.Request) { t.Errorf("unexpected network %s", r.URL.Path) })
	for _, input := range []JobInput{{}, {WorkspaceID: "ws", RequestID: "id", Prompt: " "}} {
		if _, err := c.StartJob(context.Background(), "chat", input); err == nil {
			t.Fatal(input)
		}
	}
	for _, mutate := range []func(*ChoiceInput){func(i *ChoiceInput) { i.RequestID = "" }, func(i *ChoiceInput) { i.Question = " " }, func(i *ChoiceInput) { i.Context = json.RawMessage(`null`) }, func(i *ChoiceInput) { i.Context = json.RawMessage(`{`) }, func(i *ChoiceInput) { i.Choices = i.Choices[:1] }, func(i *ChoiceInput) { i.Choices[1].Label = i.Choices[0].Label }, func(i *ChoiceInput) { i.Choices[0].Label = " " }} {
		input := verkChoiceInput()
		mutate(&input)
		if _, err := c.ClassifyChoice(context.Background(), "chat", input); err == nil {
			t.Fatal(input)
		}
	}
	if *calls != 0 {
		t.Fatal(*calls)
	}
}
