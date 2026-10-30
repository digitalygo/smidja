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

func TestStrictCEOAdmission(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			admissions := 0
			client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/capabilities") {
					features := []string{}
					if supported {
						features = append(features, "strict_model")
					}
					json.NewEncoder(w).Encode(Capabilities{ProtocolVersion: 3, ContractVersion: 3, Features: features})
					return
				}
				admissions++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["strict_model"] != true || body["message"] != "hello" {
					t.Fatal(body)
				}
				w.WriteHeader(422)
				fmt.Fprint(w, `{"error":{"code":"strict_model_unavailable","message":"configured model unavailable"}}`)
			})
			_, err := client.StartTurn(context.Background(), "chat", "hello")
			if !supported {
				if !errors.Is(err, ErrUnsupportedFeature) || admissions != 0 {
					t.Fatal(err, admissions)
				}
			} else {
				var api *APIError
				if !errors.As(err, &api) || api.Code != "strict_model_unavailable" || admissions != 1 {
					t.Fatal(err, admissions)
				}
			}
		})
	}
}

func TestDelegationStrictPolicy(t *testing.T) {
	for _, supported := range []bool{false, true} {
		for _, policy := range []*bool{nil, boolPointer(false), boolPointer(true)} {
			t.Run(fmt.Sprintf("%v/%v", supported, policy), func(t *testing.T) {
				admissions := 0
				client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/capabilities") {
						features := []string{"cross_team_delegation", "delegation_notify_origin"}
						if supported {
							features = append(features, "strict_model")
						}
						json.NewEncoder(w).Encode(Capabilities{ProtocolVersion: 3, ContractVersion: 3, Features: features})
						return
					}
					admissions++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					value, present := body["strict_model"]
					if policy == nil && present || policy != nil && (!present || value != *policy) {
						t.Fatal(body)
					}
					if body["notify_origin"] != false {
						t.Fatal(body)
					}
					fmt.Fprint(w, `{"task_id":"task","target_chat_id":"target","turn_id":"turn"}`)
				})
				_, err := client.Delegate(context.Background(), "chat", DelegationInput{WorkspaceID: "workspace", EventID: "event", OriginTurnID: "origin", TargetChatID: "target", Prompt: "prompt", StrictModel: policy, NotifyOrigin: boolPointer(false)})
				if !supported && policy != nil && *policy {
					if !errors.Is(err, ErrUnsupportedFeature) || admissions != 0 {
						t.Fatal(err, admissions)
					}
				} else if err != nil || admissions != 1 {
					t.Fatal(err, admissions)
				}
			})
		}
	}
}

func boolPointer(value bool) *bool { return &value }

func TestNativeTaskPreservesResultTurn(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task_id":"task","origin_turn_id":"origin","status":"completed","result_turn_id":"synthesis","output":"complete job"}`)
	})
	result, err := client.Task(context.Background(), "origin", "task")
	if err != nil || result.ResultTurnID != "synthesis" || result.Output != "complete job" {
		t.Fatal(result, err)
	}
}
