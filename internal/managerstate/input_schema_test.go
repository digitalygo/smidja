package managerstate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInputSchemaVocabulary(t *testing.T) {
	cases := []struct {
		schema, good string
		bad          []string
	}{
		{`{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":2,"maxLength":3}},"additionalProperties":false}`, `{"name":"é界"}`, []string{`{}`, `{"name":"x"}`, `{"name":"long"}`, `{"name":3}`, `{"name":"ok","extra":0}`, `[]`}},
		{`{"type":"array","items":{"type":"integer","minimum":1,"maximum":3},"minItems":1,"maxItems":2}`, `[1,3]`, []string{`[]`, `[1,2,3]`, `[0]`, `[4]`, `[1.5]`, `["1"]`}},
		{`{"enum":[null,true,"x",1,[1],{"a":1}]}`, `1.0`, []string{`false`, `"y"`, `2`, `[2]`, `{"a":2}`, `{"b":1}`, `{"a":1,"b":2}`}},
		{`{"const":{"a":[1,true,null]}}`, `{"a":[1.0,true,null]}`, []string{`[]`, `{"a":[1]}`, `{"a":[1,false,null]}`}},
		{`{"type":"null"}`, `null`, []string{`false`}},
		{`{"type":"boolean"}`, `true`, []string{`1`}},
		{`{"type":"number","maximum":1e4096}`, `1e4096`, []string{`1e4097`}},
		{`{"minimum":0}`, `"not numeric"`, []string{`-1`}},
		{`{"type":"object"}`, `{"unrestricted":[true]}`, nil},
		{`{"type":"array"}`, `[true]`, nil},
	}
	for _, tc := range cases {
		if err := ValidateWorkflowInputs(json.RawMessage(tc.schema), json.RawMessage(tc.good)); err != nil {
			t.Fatalf("%s: %v", tc.schema, err)
		}
		for _, bad := range tc.bad {
			if err := ValidateWorkflowInputs(json.RawMessage(tc.schema), json.RawMessage(bad)); err == nil {
				t.Fatalf("accepted %s against %s", bad, tc.schema)
			}
		}
	}
}
func TestInputSchemaRejectsMalformedAndUnsupported(t *testing.T) {
	if ValidateWorkflowInputs(json.RawMessage(`{"$ref":"unsupported"}`), json.RawMessage(`{}`)) == nil {
		t.Fatal("invalid schema admitted")
	}
	invalid := []string{`{`, `{} {}`, `true`, `null`, `[]`, `{"$ref":"remote"}`, `{"type":[]}`, `{"type":"unknown"}`, `{"title":1}`, `{"description":false}`, `{"properties":[]}`, `{"properties":{"x":false}}`, `{"items":false}`, `{"additionalProperties":{}}`, `{"required":true}`, `{"required":[1]}`, `{"required":["x","x"]}`, `{"enum":[]}`, `{"enum":true}`, `{"enum":[1,1.0]}`, `{"minimum":true}`, `{"maximum":"1"}`, `{"minLength":-1}`, `{"maxItems":1.2}`, `{"minItems":9223372036854775808}`, `{"const":1e9999}`, `{"enum":[{"x":[1e9999]}]}`}
	deep := `{}`
	for i := 0; i < 34; i++ {
		deep = `{"items":` + deep + `}`
	}
	invalid = append(invalid, deep, strings.Repeat(" ", MaxInputSchemaBytes)+`{}`)
	for _, schema := range invalid {
		if ValidateInputSchema(json.RawMessage(schema)) == nil {
			t.Fatal("accepted", schema)
		}
	}
	for _, schema := range []string{`{}`, `{"maximum":10}`, `{"type":"number"}`, ""} {
		for _, value := range []string{`1e9999`, `1e-9999`, `{"x":[1e9999]}`, strings.Repeat("1", 4097), `{`, `{} {}`, strings.Repeat(" ", MaxWorkflowInputBytes) + `{}`} {
			if ValidateWorkflowInputs(json.RawMessage(schema), json.RawMessage(value)) == nil {
				t.Fatal("accepted unsupported input", schema)
			}
		}
	}
	for _, schema := range []string{`{}`, `{"title":"x","description":"y","required":[],"additionalProperties":true}`, `{"enum":[false,true,null,"x",1,{},[]]}`} {
		if err := ValidateInputSchema(json.RawMessage(schema)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateWorkflowInputs(nil, json.RawMessage(`1e4096`)); err != nil {
		t.Fatal(err)
	}
	if ValidateWorkflowInputs(json.RawMessage(`{"maximum":10}`), json.RawMessage(`1e4096`)) == nil {
		t.Fatal("bound ignored")
	}
}
func TestWorkflowPolicyBeforePersistence(t *testing.T) {
	s, _ := openTestStore(t)
	w := workflow()
	w.InputSchema = json.RawMessage(`{"type":"object","required":["request"]}`)
	w.AllowedInvokers = []string{"ceo", "team:private:review"}
	if _, err := s.CreateTask(StartTask{Workflow: w, Inputs: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("invalid input admitted")
	}
	if len(s.ListTasks()) != 0 {
		t.Fatal("invalid task persisted")
	}
	task, err := s.CreateTask(StartTask{Workflow: w, Inputs: json.RawMessage(`{"request":"work"}`)})
	if err != nil {
		t.Fatal(err)
	}
	w.AllowedInvokers[0] = "changed"
	w.InputSchema = json.RawMessage(`{}`)
	pinned, _ := s.GetTask(task.ID)
	if pinned.Workflow.AllowedInvokers[0] != "ceo" || string(pinned.Workflow.InputSchema) == `{}` {
		t.Fatal("policy not pinned")
	}
	for _, invokers := range [][]string{{"ceo", "ceo"}, {"arbitrary"}, {"team:"}, {"team: "}} {
		bad := workflow()
		bad.AllowedInvokers = invokers
		if ValidateWorkflow(bad) == nil {
			t.Fatal(invokers)
		}
	}
	bad := workflow()
	bad.InputSchema = json.RawMessage(`{"$ref":"bad"}`)
	if ValidateWorkflow(bad) == nil {
		t.Fatal("bad schema")
	}
}
func TestExactJSONComparisonAndNumericDomain(t *testing.T) {
	for _, pair := range [][2]string{{`1`, `1.0`}, {`[1]`, `[1.0]`}, {`{"a":1}`, `{"a":1.0}`}, {`null`, `null`}, {`true`, `true`}, {`"x"`, `"x"`}} {
		a, _ := schemaJSON([]byte(pair[0]))
		b, _ := schemaJSON([]byte(pair[1]))
		if !sameJSON(a, b) {
			t.Fatal(pair)
		}
	}
	for _, pair := range [][2]string{{`1`, `true`}, {`[]`, `{}`}, {`[1]`, `[]`}, {`{}`, `[]`}, {`{"x":1}`, `{"y":1}`}, {`"x"`, `false`}, {`false`, `null`}} {
		a, _ := schemaJSON([]byte(pair[0]))
		b, _ := schemaJSON([]byte(pair[1]))
		if sameJSON(a, b) {
			t.Fatal(pair)
		}
	}
	if sameJSON(struct{}{}, struct{}{}) {
		t.Fatal("unsupported Go value")
	}
	if _, ok := number(json.Number("1e99999999999999999999")); ok {
		t.Fatal("overflow")
	}
}
