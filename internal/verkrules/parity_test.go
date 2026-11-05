package verkrules

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestDesignMockParity(t *testing.T) {
	data, err := os.ReadFile("testdata/mock-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		SourceSHA256 string `json:"source_sha256"`
		Evaluations  []struct {
			Name     string
			Rule     *Rule
			Refs     map[string]Resolution
			Expected Result
		}
		Routes []struct {
			Name      string
			Branches  []Branch
			DefaultID string
			Refs      map[string]Resolution
			Expected  RouteResult
		}
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.SourceSHA256) != 64 || len(cases.Evaluations) != 80 || len(cases.Routes) != 24 {
		t.Fatal("incomplete oracle fixture")
	}
	resolver := func(refs map[string]Resolution) Resolver {
		return func(path string) (Resolution, error) { return refs[path], nil }
	}
	for _, tc := range cases.Evaluations {
		t.Run(tc.Name, func(t *testing.T) {
			got := Evaluate(tc.Rule, resolver(tc.Refs))
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("got %+v, expected %+v", got, tc.Expected)
			}
		})
	}
	for _, tc := range cases.Routes {
		t.Run(tc.Name, func(t *testing.T) {
			got := Route(tc.Branches, tc.DefaultID, resolver(tc.Refs))
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("got %+v, expected %+v", got, tc.Expected)
			}
		})
	}
}
