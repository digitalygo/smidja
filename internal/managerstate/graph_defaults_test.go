package managerstate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/verk"
)

func TestGraphDefaultInputsPersistBeforeSchemaValidation(t *testing.T) {
	store, dir := openTestStore(t)
	workflow := graphWorkflow()
	workflow.InputSchema = json.RawMessage(`{"type":"object","required":["goal","number"],"properties":{"goal":{"type":"string"},"number":{"type":"integer"}}}`)
	workflow.Graph.InputValues = []verk.Value{{Name: "goal", Type: "string", Value: "default goal"}, {Name: "number", Type: "number", Value: json.Number("9007199254740993")}}
	start := StartTask{Workflow: workflow, Inputs: json.RawMessage(`{}`), InvocationKey: "defaulted"}
	task, err := store.CreateTask(start)
	if err != nil {
		t.Fatal(err)
	}
	if task.GraphState.Inputs["goal"] != "default goal" || !strings.Contains(string(task.Inputs), "9007199254740993") {
		t.Fatal(task.Inputs, task.GraphState.Inputs)
	}
	effective, _ := json.Marshal(task.GraphState.Inputs)
	if string(effective) != string(task.Inputs) {
		t.Fatal("task/state inputs diverged", string(task.Inputs), string(effective))
	}
	same, err := store.CreateTask(start)
	if err != nil || same.ID != task.ID {
		t.Fatal("defaulted retry not idempotent", same, err)
	}
	start.Inputs = json.RawMessage(`{"goal":"explicit goal","number":0}`)
	start.InvocationKey = "explicit"
	explicit, err := store.CreateTask(start)
	if err != nil || explicit.GraphState.Inputs["goal"] != "explicit goal" || explicit.GraphState.Inputs["number"] != json.Number("0") {
		t.Fatal(explicit, err)
	}
	workflow.Graph.InputValues[0].Value = "changed after creation"
	task.GraphState.Inputs["goal"] = "external mutation"
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := store.GetTask(task.ID)
	if err != nil || restored.GraphState.Inputs["goal"] != "default goal" {
		t.Fatal(restored, err)
	}
	restoredInputs, _ := json.Marshal(restored.GraphState.Inputs)
	if string(restoredInputs) != string(restored.Inputs) {
		t.Fatal("replay inputs diverged")
	}
}

func TestGraphDefaultInputsDoNotHideExplicitInvalidValues(t *testing.T) {
	store, _ := openTestStore(t)
	workflow := graphWorkflow()
	workflow.Graph.InputValues = []verk.Value{{Name: "goal", Type: "string", Value: "default"}}
	workflow.InputSchema = json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"}}}`)
	for _, input := range []string{`null`, `[]`, `{"goal":null}`, `{"goal":12}`, `{}`} {
		task, err := store.CreateTask(StartTask{Workflow: workflow, Inputs: json.RawMessage(input)})
		if input == `{}` {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil {
			t.Fatal("invalid explicit input accepted", input, task)
		}
	}
}
