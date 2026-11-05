package managerpackages

import "testing"

func TestWorkflowPolicyRejectedBeforeActivation(t *testing.T) {
	m := Manifest{ID: "example.review", Workflows: []string{"workflow.json"}}
	for _, document := range []string{`{"id":"example.review/review","input_schema":{"$ref":"remote"}}`, `{"id":"example.review/review","allowed_invokers":["unknown"]}`, `{"id":"example.review/review","allowed_invokers":true}`, `{`} {
		if validateCatalog(m, map[string][]byte{"workflow.json": []byte(document)}) == nil {
			t.Fatal(document)
		}
	}
	if err := validateCatalog(m, map[string][]byte{"workflow.json": []byte(`{"id":"example.review/review","input_schema":{"type":"object"},"allowed_invokers":["ceo"]}`)}); err != nil {
		t.Fatal(err)
	}
}
