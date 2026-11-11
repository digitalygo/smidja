package managerstate

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestToolReceiptsSurviveReplay(t *testing.T) {
	s, dir := openTestStore(t)
	op, created, err := s.BeginTool("call", "chat", "turn", "write", json.RawMessage(`{"path":"/tmp/a","body":"hello"}`), "laptop")
	if err != nil || !created || op.Status != Running {
		t.Fatal(op, created, err)
	}
	same, created, err := s.BeginTool("call", "chat", "turn", "write", json.RawMessage(`{ "body": "hello", "path": "/tmp/a" }`), "laptop")
	if err != nil || created || same.Status != Running {
		t.Fatal("pending replay may not execute", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	existing, err := reopened.GetTool("call")
	if err != nil || !reflect.DeepEqual(op, existing) {
		t.Fatal("pending receipt lost", err)
	}
	output := "\noriginal tool output ✓\n"
	if err = reopened.FinishTool("call", output, true); err != nil {
		t.Fatal(err)
	}
	result, created, err := reopened.BeginTool("call", "chat", "turn", "write", json.RawMessage(`{"path":"/tmp/a","body":"hello"}`), "laptop")
	if err != nil || created || result.Status != Completed || result.Output != output || !result.IsError {
		t.Fatal("completed receipt lost", err)
	}
	if err = reopened.FinishTool("call", "different", false); !errors.Is(err, ErrConflict) {
		t.Fatal("finished overwritten")
	}
	reopened.Close()
	final, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer final.Close()
	replayed, err := final.GetTool("call")
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatal("result replay mismatch", err)
	}
}

func TestToolBindingAndArgumentValidation(t *testing.T) {
	s, _ := openTestStore(t)
	if _, _, err := s.BeginTool("call", "chat", "turn", "read", nil, "manager"); err != nil {
		t.Fatal(err)
	}
	for _, args := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`oops`), json.RawMessage(`{} {}`)} {
		if _, _, err := s.BeginTool("other", "chat", "turn", "read", args, "manager"); err == nil {
			t.Fatal("bad args accepted")
		}
	}
	variants := [][]string{{"other", "turn", "read", "manager"}, {"chat", "other", "read", "manager"}, {"chat", "turn", "write", "manager"}, {"chat", "turn", "read", "other"}}
	for _, v := range variants {
		if _, _, err := s.BeginTool("call", v[0], v[1], v[2], nil, v[3]); !errors.Is(err, ErrConflict) {
			t.Fatal("binding changed", err)
		}
	}
	if _, _, err := s.BeginTool("call", "chat", "turn", "read", json.RawMessage(`{"different":1}`), "manager"); !errors.Is(err, ErrConflict) {
		t.Fatal("args changed")
	}
	if _, _, err := s.BeginTool("new", "", "turn", "read", nil, "manager"); err == nil {
		t.Fatal("missing binding")
	}
	if _, _, err := s.BeginTool("", "chat", "turn", "read", nil, "manager"); err == nil {
		t.Fatal("missing callID")
	}
	if _, err := s.GetTool("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.FinishTool("missing", "", false); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
