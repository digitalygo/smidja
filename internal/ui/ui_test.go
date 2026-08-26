package ui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/sdk"
)

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func newTest(t *testing.T, script string, mode sdk.Mode) (*LineUI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errBuf bytes.Buffer
	l := New(strings.NewReader(script), &out, &errBuf, mode)
	return l, &out, &errBuf
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"yes lowercase", "y\n", true},
		{"yes uppercase", "Y\n", true},
		{"yes word", "yes\n", true},
		{"yes word uppercase", "YES\n", true},
		{"yes padded", "  yes  \n", true},
		{"no", "n\n", false},
		{"no word", "no\n", false},
		{"empty line", "\n", false},
		{"junk", "maybe\n", false},
		{"eof no input", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, out, errBuf := newTest(t, c.in, sdk.ModeInteractive)
			got, err := l.Confirm("title", "message")
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			if got != c.want {
				t.Errorf("Confirm = %v, want %v", got, c.want)
			}
			if !strings.Contains(errBuf.String(), "[y/N]") {
				t.Errorf("stderr %q does not show the [y/N] prompt", errBuf.String())
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty (model output must stay clean)", out.String())
			}
		})
	}
}

func TestConfirmPartialLineAtEOF(t *testing.T) {
	l, _, _ := newTest(t, "y", sdk.ModeInteractive)
	got, err := l.Confirm("t", "m")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !got {
		t.Error("Confirm with partial 'y' at EOF = false, want true")
	}
}

func TestConfirmPrintMode(t *testing.T) {
	cr := &countingReader{r: strings.NewReader("y\n")}
	var out, errBuf bytes.Buffer
	l := New(cr, &out, &errBuf, sdk.ModePrint)

	got, err := l.Confirm("t", "m")
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Confirm error = %v, want sdk.ErrModeUnsupported", err)
	}
	if got {
		t.Error("Confirm in print mode = true, want false")
	}
	if cr.n != 0 {
		t.Errorf("print mode read %d bytes from stdin", cr.n)
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr = %q, want empty in print mode", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestSelect(t *testing.T) {
	opts := []string{"alpha", "beta", "gamma"}

	t.Run("valid", func(t *testing.T) {
		l, _, errBuf := newTest(t, "2\n", sdk.ModeInteractive)
		got, err := l.Select("pick", opts)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "beta" {
			t.Errorf("Select = %q, want %q", got, "beta")
		}
		s := errBuf.String()
		if !strings.Contains(s, "1. alpha") || !strings.Contains(s, "2. beta") || !strings.Contains(s, "3. gamma") {
			t.Errorf("stderr %q does not show the numbered list", s)
		}
	})

	t.Run("out of range re-prompts", func(t *testing.T) {
		l, _, errBuf := newTest(t, "99\n3\n", sdk.ModeInteractive)
		got, err := l.Select("pick", opts)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "gamma" {
			t.Errorf("Select = %q, want %q", got, "gamma")
		}
		if !strings.Contains(errBuf.String(), "invalid choice") {
			t.Errorf("stderr %q does not report the invalid choice", errBuf.String())
		}
	})

	t.Run("non numeric re-prompts", func(t *testing.T) {
		l, _, _ := newTest(t, "abc\n1\n", sdk.ModeInteractive)
		got, err := l.Select("pick", opts)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "alpha" {
			t.Errorf("Select = %q, want %q", got, "alpha")
		}
	})

	t.Run("zero re-prompts", func(t *testing.T) {
		l, _, _ := newTest(t, "0\n2\n", sdk.ModeInteractive)
		got, err := l.Select("pick", opts)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "beta" {
			t.Errorf("Select = %q, want %q", got, "beta")
		}
	})

	t.Run("eof cancels", func(t *testing.T) {
		l, _, _ := newTest(t, "", sdk.ModeInteractive)
		got, err := l.Select("pick", opts)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "" {
			t.Errorf("Select = %q, want empty on EOF", got)
		}
	})

	t.Run("eof after invalid cancels", func(t *testing.T) {
		l, _, _ := newTest(t, "99\n", sdk.ModeInteractive)
		got, err := l.Select("pick", opts)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "" {
			t.Errorf("Select = %q, want empty on EOF after invalid input", got)
		}
	})

	t.Run("empty options", func(t *testing.T) {
		l, _, errBuf := newTest(t, "", sdk.ModeInteractive)
		got, err := l.Select("pick", nil)
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got != "" {
			t.Errorf("Select = %q, want empty for no options", got)
		}
		if strings.Contains(errBuf.String(), "choose") {
			t.Errorf("stderr %q prompts for an empty option list", errBuf.String())
		}
	})

	t.Run("stdout stays clean", func(t *testing.T) {
		l, out, _ := newTest(t, "1\n", sdk.ModeInteractive)
		if _, err := l.Select("pick", opts); err != nil {
			t.Fatalf("Select: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
	})
}

func TestSelectPrintMode(t *testing.T) {
	cr := &countingReader{r: strings.NewReader("1\n")}
	var out, errBuf bytes.Buffer
	l := New(cr, &out, &errBuf, sdk.ModePrint)

	got, err := l.Select("pick", []string{"alpha"})
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Select error = %v, want sdk.ErrModeUnsupported", err)
	}
	if got != "" {
		t.Errorf("Select = %q, want empty", got)
	}
	if cr.n != 0 {
		t.Errorf("print mode read %d bytes from stdin", cr.n)
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr = %q, want empty in print mode", errBuf.String())
	}
}

func TestInput(t *testing.T) {
	t.Run("plain line", func(t *testing.T) {
		l, _, _ := newTest(t, "hello\n", sdk.ModeInteractive)
		got, err := l.Input("name", "")
		if err != nil {
			t.Fatalf("Input: %v", err)
		}
		if got != "hello" {
			t.Errorf("Input = %q, want %q", got, "hello")
		}
	})

	t.Run("preserves inner spacing", func(t *testing.T) {
		l, _, _ := newTest(t, "  hello world  \n", sdk.ModeInteractive)
		got, err := l.Input("name", "")
		if err != nil {
			t.Fatalf("Input: %v", err)
		}
		if got != "  hello world  " {
			t.Errorf("Input = %q, want only the newline stripped", got)
		}
	})

	t.Run("carriage return stripped", func(t *testing.T) {
		l, _, _ := newTest(t, "hello\r\n", sdk.ModeInteractive)
		got, err := l.Input("name", "")
		if err != nil {
			t.Fatalf("Input: %v", err)
		}
		if got != "hello" {
			t.Errorf("Input = %q, want %q", got, "hello")
		}
	})

	t.Run("empty line", func(t *testing.T) {
		l, _, _ := newTest(t, "\n", sdk.ModeInteractive)
		got, err := l.Input("name", "")
		if err != nil {
			t.Fatalf("Input: %v", err)
		}
		if got != "" {
			t.Errorf("Input = %q, want empty", got)
		}
	})

	t.Run("eof cancels", func(t *testing.T) {
		l, _, _ := newTest(t, "", sdk.ModeInteractive)
		got, err := l.Input("name", "")
		if err != nil {
			t.Fatalf("Input: %v", err)
		}
		if got != "" {
			t.Errorf("Input = %q, want empty on EOF", got)
		}
	})

	t.Run("placeholder rendered", func(t *testing.T) {
		l, _, errBuf := newTest(t, "x\n", sdk.ModeInteractive)
		if _, err := l.Input("name", "e.g. acme"); err != nil {
			t.Fatalf("Input: %v", err)
		}
		if !strings.Contains(errBuf.String(), "name [e.g. acme]: ") {
			t.Errorf("stderr %q does not show the placeholder prompt", errBuf.String())
		}
	})

	t.Run("stdout stays clean", func(t *testing.T) {
		l, out, _ := newTest(t, "x\n", sdk.ModeInteractive)
		if _, err := l.Input("name", ""); err != nil {
			t.Fatalf("Input: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
	})
}

func TestInputPrintMode(t *testing.T) {
	cr := &countingReader{r: strings.NewReader("hello\n")}
	var out, errBuf bytes.Buffer
	l := New(cr, &out, &errBuf, sdk.ModePrint)

	got, err := l.Input("name", "")
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Input error = %v, want sdk.ErrModeUnsupported", err)
	}
	if got != "" {
		t.Errorf("Input = %q, want empty", got)
	}
	if cr.n != 0 {
		t.Errorf("print mode read %d bytes from stdin", cr.n)
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr = %q, want empty in print mode", errBuf.String())
	}
}

func writeEditorScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write editor script: %v", err)
	}
	return path
}

func TestEditorUnset(t *testing.T) {
	t.Setenv("EDITOR", "")
	l, _, _ := newTest(t, "", sdk.ModeInteractive)
	got, err := l.Editor("title", "prefill")
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Editor error = %v, want sdk.ErrModeUnsupported", err)
	}
	if got != "" {
		t.Errorf("Editor = %q, want empty", got)
	}
	if !strings.Contains(err.Error(), "EDITOR") {
		t.Errorf("error %q should name $EDITOR", err)
	}
}

func TestEditorWithScript(t *testing.T) {
	script := writeEditorScript(t, `printf '\nedited by script\n' >> "$1"`)
	t.Setenv("EDITOR", "sh "+script)

	l, out, errBuf := newTest(t, "", sdk.ModeInteractive)
	got, err := l.Editor("title", "prefill")
	if err != nil {
		t.Fatalf("Editor: %v", err)
	}
	if want := "prefill\nedited by script\n"; got != want {
		t.Errorf("Editor = %q, want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errBuf.String())
	}
}

func TestEditorFails(t *testing.T) {
	script := writeEditorScript(t, "exit 3")
	t.Setenv("EDITOR", "sh "+script)

	l, _, _ := newTest(t, "", sdk.ModeInteractive)
	got, err := l.Editor("title", "prefill")
	if err == nil {
		t.Fatal("Editor succeeded, want an error for a failing editor")
	}
	if !strings.Contains(err.Error(), "editor") {
		t.Errorf("error %q does not mention the editor", err)
	}
	if got != "" {
		t.Errorf("Editor = %q, want empty on failure", got)
	}
}

func TestEditorPrintModeNeverRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran.marker")
	script := writeEditorScript(t, "touch "+marker)
	t.Setenv("EDITOR", "sh "+script)

	cr := &countingReader{r: strings.NewReader("")}
	var out, errBuf bytes.Buffer
	l := New(cr, &out, &errBuf, sdk.ModePrint)

	got, err := l.Editor("title", "prefill")
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Editor error = %v, want sdk.ErrModeUnsupported", err)
	}
	if got != "" {
		t.Errorf("Editor = %q, want empty", got)
	}
	if cr.n != 0 {
		t.Errorf("print mode read %d bytes from stdin", cr.n)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("the editor ran in print mode")
	}
}

func TestNotifyRouting(t *testing.T) {
	t.Run("interactive renders to stderr", func(t *testing.T) {
		l, out, errBuf := newTest(t, "", sdk.ModeInteractive)
		l.Notify("build failed", sdk.NotifyError)
		if got := errBuf.String(); !strings.Contains(got, "[error] build failed") {
			t.Errorf("stderr = %q, want a [error] notification line", got)
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
	})

	t.Run("print mode no-op", func(t *testing.T) {
		l, out, errBuf := newTest(t, "", sdk.ModePrint)
		l.Notify("build failed", sdk.NotifyError)
		if errBuf.Len() != 0 {
			t.Errorf("stderr = %q, want empty in print mode", errBuf.String())
		}
		if out.Len() != 0 {
			t.Errorf("stdout = %q, want empty", out.String())
		}
	})
}

func TestSetStatusRendersAtPromptBoundary(t *testing.T) {
	t.Run("rendered before the next dialog", func(t *testing.T) {
		l, _, errBuf := newTest(t, "y\n", sdk.ModeInteractive)
		l.SetStatus("files", "3 files")
		if errBuf.Len() != 0 {
			t.Fatalf("stderr = %q before any dialog, want deferred rendering", errBuf.String())
		}
		if _, err := l.Confirm("t", "m"); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		s := errBuf.String()
		statusIdx := strings.Index(s, "status: files: 3 files")
		promptIdx := strings.Index(s, "[y/N]")
		if statusIdx < 0 {
			t.Errorf("stderr %q lacks the status line", s)
		}
		if statusIdx > promptIdx {
			t.Errorf("status line renders after the prompt: %q", s)
		}
	})

	t.Run("cleared status renders nothing", func(t *testing.T) {
		l, _, errBuf := newTest(t, "y\n", sdk.ModeInteractive)
		l.SetStatus("files", "3 files")
		l.SetStatus("files", "")
		if _, err := l.Confirm("t", "m"); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		if strings.Contains(errBuf.String(), "status: files") {
			t.Errorf("stderr %q renders a cleared status", errBuf.String())
		}
	})

	t.Run("latest value wins", func(t *testing.T) {
		l, _, errBuf := newTest(t, "y\n", sdk.ModeInteractive)
		l.SetStatus("files", "3 files")
		l.SetStatus("files", "4 files")
		if _, err := l.Confirm("t", "m"); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		s := errBuf.String()
		if strings.Contains(s, "status: files: 3 files") {
			t.Errorf("stderr %q renders a stale status value", s)
		}
		if !strings.Contains(s, "status: files: 4 files") {
			t.Errorf("stderr %q lacks the latest status value", s)
		}
	})

	t.Run("print mode no-op", func(t *testing.T) {
		l, _, errBuf := newTest(t, "", sdk.ModePrint)
		l.SetStatus("files", "3 files")
		if errBuf.Len() != 0 {
			t.Errorf("stderr = %q, want empty in print mode", errBuf.String())
		}
	})
}

func TestStateRenderingOrder(t *testing.T) {
	l, _, errBuf := newTest(t, "ok\n", sdk.ModeInteractive)
	l.SetTitle("smidja 0.1")
	l.SetWorkingMessage("thinking hard")
	l.SetStatus("tools", "2 active")
	l.SetStatus("files", "3 files")
	l.SetWidget("progress", []string{"a", "b"})

	got, err := l.Input("name", "")
	if err != nil {
		t.Fatalf("Input: %v", err)
	}
	if got != "ok" {
		t.Errorf("Input = %q, want %q", got, "ok")
	}

	s := errBuf.String()
	order := []string{
		"title: smidja 0.1",
		"working: thinking hard",
		"status: files: 3 files",
		"status: tools: 2 active",
		"widget: progress: a | b",
		"name: ",
	}
	prev := -1
	for _, want := range order {
		idx := strings.Index(s, want)
		if idx < 0 {
			t.Errorf("stderr %q lacks %q", s, want)
			continue
		}
		if idx < prev {
			t.Errorf("stderr renders %q out of order", want)
		}
		prev = idx
	}
}

func TestSetWidgetClears(t *testing.T) {
	l, _, errBuf := newTest(t, "y\n", sdk.ModeInteractive)
	l.SetWidget("w", []string{"a", "b"})
	l.SetWidget("w", nil)
	if _, err := l.Confirm("t", "m"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if strings.Contains(errBuf.String(), "widget: w") {
		t.Errorf("stderr %q renders a cleared widget", errBuf.String())
	}
}

func TestDialogsSerialized(t *testing.T) {
	const confirms = 12
	script := strings.Repeat("y\n", confirms)

	var out, errBuf bytes.Buffer
	l := New(strings.NewReader(script), &out, &errBuf, sdk.ModeInteractive)

	var wg sync.WaitGroup
	errs := make(chan error, confirms)
	for i := 0; i < confirms; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := l.Confirm("t", "m")
			if err != nil {
				errs <- err
				return
			}
			if !ok {
				errs <- errors.New("Confirm answered no")
			}
		}()
	}
	for i := 0; i < confirms; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Notify("notice", sdk.NotifyInfo)
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.SetStatus("k", "v")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent dialog: %v", err)
	}

	s := errBuf.String()
	if got := strings.Count(s, "[y/N]"); got != confirms {
		t.Errorf("stderr has %d confirm prompts, want %d", got, confirms)
	}
	if got := strings.Count(s, "smidja: [info] notice"); got != confirms {
		t.Errorf("stderr has %d notify lines, want %d", got, confirms)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestNilStreamsTolerated(t *testing.T) {
	l := New(nil, nil, nil, sdk.ModeInteractive)

	got, err := l.Confirm("t", "m")
	if err != nil || got {
		t.Errorf("Confirm on nil stdin = %v, %v; want false, nil", got, err)
	}
	l.Notify("n", sdk.NotifyInfo)
	l.SetStatus("k", "v")
	l.SetTitle("t")
}

func TestUnknownModeBehavesLikePrint(t *testing.T) {
	var out, errBuf bytes.Buffer
	l := New(strings.NewReader("y\n"), &out, &errBuf, sdk.Mode("future-mode"))

	if _, err := l.Confirm("t", "m"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Confirm error = %v, want sdk.ErrModeUnsupported for an unknown mode", err)
	}
	l.Notify("n", sdk.NotifyInfo)
	l.SetStatus("k", "v")
	if errBuf.Len() != 0 {
		t.Errorf("stderr = %q, want empty for an unknown mode", errBuf.String())
	}
}
