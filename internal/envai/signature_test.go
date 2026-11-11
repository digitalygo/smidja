package envai

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestVerifyToolRequest(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	args := `{"z":1e-7,"a":"text"}`
	signed := []byte("call\ntool\n" + `{"a":"text","z":1e-7}`)
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, signed))
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/.well-known/jwks.json" || r.Header.Get("Authorization") != "" {
			t.Error("JWKS must use same origin without bearer")
		}
		fmt.Fprintf(w, `{"keys":[{"kid":"key","kty":"OKP","crv":"Ed25519","alg":"EdDSA","x":%q}]}`, base64.RawURLEncoding.EncodeToString(public))
	})
	makeEvent := func(req ToolRequest) Event {
		data, _ := json.Marshal(req)
		return Event{Type: "agent.tool.request", ChatID: "chat", TurnID: "turn", Agent: &Agent{InstanceID: "agent"}, Data: data}
	}
	req := ToolRequest{CallID: "call", ToolName: "tool", Kind: "client", Args: json.RawMessage(args), Signature: Signature{Alg: "EdDSA", KeyID: "key", Value: signature}}
	for i := 0; i < 2; i++ {
		if _, err := c.VerifyToolRequest(context.Background(), makeEvent(req)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("key not cached", calls)
	}
	for _, mutate := range []func(*ToolRequest){
		func(r *ToolRequest) { r.Kind = "server" }, func(r *ToolRequest) { r.CallID = "call\n" }, func(r *ToolRequest) { r.Signature.Alg = "none" }, func(r *ToolRequest) { r.Signature.Value = "bad" }, func(r *ToolRequest) { r.Signature.KeyID = "unknown" }, func(r *ToolRequest) { r.Args = json.RawMessage(`{"a":"tampered"}`) }, func(r *ToolRequest) { r.Args = json.RawMessage(`42`) },
	} {
		other := req
		mutate(&other)
		if _, err := c.VerifyToolRequest(context.Background(), makeEvent(other)); err == nil {
			t.Fatal("accepted invalid signature", other)
		}
	}
	if _, err := c.VerifyToolRequest(context.Background(), Event{}); err == nil {
		t.Fatal("accepted bad event")
	}
}
func TestRejectInvalidJWKS(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(nil)
	valid := fmt.Sprintf(`{"kid":"key","kty":"OKP","crv":"Ed25519","alg":"EdDSA","x":%q}`, base64.RawURLEncoding.EncodeToString(public))
	for _, body := range []string{`{"keys":[]}`, `{"keys":[{"kid":"key","kty":"RSA"}]}`, `{"keys":[{"kid":"key","kty":"OKP","crv":"Ed25519","alg":"EdDSA","x":"bad"}]}`, `{"keys":[` + valid + `,` + valid + `]}`, `bad`} {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := c.signingKey(context.Background(), "key"); err == nil {
			t.Fatal("accepted invalid keys", body)
		}
	}
}

func TestVerifyDetachedToolRequest(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte("call\ntool\n{}")))
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"keys":[{"kid":"key","kty":"OKP","crv":"Ed25519","alg":"EdDSA","x":%q}]}`, base64.RawURLEncoding.EncodeToString(public))
	})
	request := ToolRequest{CallID: "call", ToolName: "tool", Kind: "client", Args: json.RawMessage(`{}`), Signature: Signature{Alg: "EdDSA", KeyID: "key", Value: signature}}
	raw, _ := json.Marshal(request)
	payload, _ := json.Marshal(DetachedEvent{TaskID: "task", OriginTurnID: "origin", EventType: "agent.tool.request", EventPayload: raw})
	event := Event{Type: "task.event", ChatID: "chat", Agent: &Agent{InstanceID: "agent"}, Data: payload}
	if got, err := c.VerifyDetachedToolRequest(context.Background(), event, "task", "origin"); err != nil || got.CallID != "call" {
		t.Fatal(got, err)
	}
	for _, ids := range [][2]string{{"wrong", "origin"}, {"task", "wrong"}, {"", "origin"}} {
		if _, err := c.VerifyDetachedToolRequest(context.Background(), event, ids[0], ids[1]); err == nil {
			t.Fatal(ids)
		}
	}
	event.TurnID = "closed-turn"
	if _, err := c.VerifyDetachedToolRequest(context.Background(), event, "task", "origin"); err == nil {
		t.Fatal("accepted old-turn envelope")
	}
	event.TurnID = ""
	event.Data = json.RawMessage(`bad`)
	if _, err := c.VerifyDetachedToolRequest(context.Background(), event, "task", "origin"); err == nil {
		t.Fatal("accepted invalid JSON")
	}
}
