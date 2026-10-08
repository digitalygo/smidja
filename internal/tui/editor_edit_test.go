package tui

import (
	"strings"
	"testing"
)

func TestDeleteCharBackwardForward(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("hello")
	buffer.SetCursor(0, 5)
	buffer.DeleteCharBackward()
	if buffer.Text() != "hell" {
		t.Fatalf("backspace = %q", buffer.Text())
	}
	buffer.DeleteCharForward()
	if buffer.Text() != "hell" {
		t.Fatalf("forward at end should not change")
	}
	buffer.SetCursor(0, 0)
	buffer.DeleteCharBackward()
	if buffer.Text() != "hell" {
		t.Fatalf("backspace at start should not change")
	}
	buffer.SetCursor(0, 0)
	buffer.DeleteCharForward()
	if buffer.Text() != "ell" {
		t.Fatalf("forward = %q", buffer.Text())
	}
	buffer.SetText("ab\ncd")
	buffer.SetCursor(1, 0)
	buffer.DeleteCharBackward()
	if buffer.Text() != "abcd" {
		t.Fatalf("merge backspace = %q", buffer.Text())
	}
	buffer.SetText("ab\ncd")
	buffer.SetCursor(0, 2)
	buffer.DeleteCharForward()
	if buffer.Text() != "abcd" {
		t.Fatalf("merge forward = %q", buffer.Text())
	}
	buffer.SetText("héllo")
	buffer.SetCursor(0, 3)
	buffer.DeleteCharBackward()
	if buffer.Text() != "hllo" {
		t.Fatalf("grapheme backspace = %q, want %q", buffer.Text(), "hllo")
	}
}

func TestDeleteMarkerBackwardForward(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.HandlePaste(strings.Repeat("line\n", 12))
	markerLine := buffer.Lines()[0]
	buffer.SetCursor(0, len(markerLine))
	buffer.DeleteCharBackward()
	if strings.Contains(buffer.Text(), "[paste #") {
		t.Fatalf("marker should be deleted: %q", buffer.Text())
	}
	if len(buffer.pastes) != 0 {
		t.Fatalf("pastes should be cleared")
	}
	buffer.HandlePaste(strings.Repeat("z\n", 12))
	markerLine = buffer.Lines()[0]
	mid := len("[paste #1")
	buffer.SetCursor(0, mid)
	buffer.DeleteCharBackward()
	if strings.Contains(buffer.Text(), "[paste #") {
		t.Fatalf("mid-marker backspace should delete whole marker")
	}
	buffer.HandlePaste(strings.Repeat("q\n", 12))
	buffer.SetCursor(0, 0)
	buffer.DeleteCharForward()
	if strings.Contains(buffer.Text(), "[paste #") {
		t.Fatalf("forward marker delete failed: %q", buffer.Text())
	}
}

func TestRenumberPasteMarkers(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.HandlePaste(strings.Repeat("a\n", 12))
	buffer.InsertChar(" ")
	buffer.HandlePaste(strings.Repeat("b\n", 12))
	if len(buffer.pastes) != 2 {
		t.Fatalf("pastes = %d", len(buffer.pastes))
	}
	firstMarker := ""
	for _, line := range buffer.Lines() {
		if strings.Contains(line, "[paste #1") {
			firstMarker = line
		}
	}
	if firstMarker == "" {
		t.Fatalf("first marker missing: %q", buffer.Text())
	}
	idx := strings.Index(firstMarker, "[paste #1")
	end := strings.Index(firstMarker[idx:], "]") + idx + 1
	markerText := firstMarker[idx:end]
	lineIdx := 0
	for i, line := range buffer.Lines() {
		if strings.Contains(line, markerText) {
			lineIdx = i
			break
		}
	}
	buffer.SetCursor(lineIdx, strings.Index(buffer.Lines()[lineIdx], markerText)+len(markerText))
	buffer.DeleteCharBackward()
	if len(buffer.pastes) != 1 {
		t.Fatalf("after delete pastes = %d", len(buffer.pastes))
	}
	if !strings.Contains(buffer.Text(), "[paste #1") {
		t.Fatalf("renumbered marker missing: %q", buffer.Text())
	}
	empty := newEditorBuffer()
	empty.renumberPasteMarkers()
}

func TestDeleteWordOperations(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("alpha beta gamma")
	buffer.SetCursor(0, 11)
	buffer.DeleteWordBackward()
	if buffer.Text() != "alpha gamma" {
		t.Fatalf("word backward = %q", buffer.Text())
	}
	buffer.SetCursor(0, 6)
	buffer.DeleteWordForward()
	if buffer.Text() != "alpha " {
		t.Fatalf("word forward = %q", buffer.Text())
	}
	buffer.SetCursor(0, 0)
	buffer.DeleteWordBackward()
	buffer.SetText("one\ntwo")
	buffer.SetCursor(1, 0)
	buffer.DeleteWordBackward()
	if buffer.Text() != "onetwo" {
		t.Fatalf("word backward merge = %q", buffer.Text())
	}
	buffer.SetText("one\ntwo")
	buffer.SetCursor(0, 3)
	buffer.DeleteWordForward()
	if buffer.Text() != "onetwo" {
		t.Fatalf("word forward merge = %q", buffer.Text())
	}
	buffer.SetText("hello")
	buffer.SetCursor(0, 5)
	buffer.DeleteWordForward()
	if buffer.Text() != "hello" {
		t.Fatalf("word forward at end should not change")
	}
}

func TestDeleteToLineOperations(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("hello world")
	buffer.SetCursor(0, 5)
	buffer.DeleteToLineStart()
	if buffer.Text() != " world" {
		t.Fatalf("to start = %q", buffer.Text())
	}
	buffer.SetCursor(0, 0)
	buffer.DeleteToLineEnd()
	if buffer.Text() != "" {
		t.Fatalf("to end = %q", buffer.Text())
	}
	buffer.SetText("ab\ncd")
	buffer.SetCursor(1, 0)
	buffer.DeleteToLineStart()
	if buffer.Text() != "abcd" {
		t.Fatalf("to start merge = %q", buffer.Text())
	}
	buffer.SetText("ab\ncd")
	buffer.SetCursor(0, 2)
	buffer.DeleteToLineEnd()
	if buffer.Text() != "ab\ncd" && buffer.Text() != "abcd" {
		t.Fatalf("to end merge = %q", buffer.Text())
	}
	buffer.SetText("x")
	buffer.SetCursor(0, 0)
	buffer.DeleteToLineStart()
	buffer.SetCursor(0, 1)
	buffer.DeleteToLineEnd()
	buffer.SetText("x\ny")
	buffer.SetCursor(1, 1)
	buffer.DeleteToLineEnd()
	if buffer.Text() != "x\ny" {
		t.Fatalf("to end at last line end should not change: %q", buffer.Text())
	}
}

func TestYankAndYankPop(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.Yank()
	buffer.YankPop()
	buffer.SetText("one two three four")
	buffer.SetCursor(0, 19)
	buffer.DeleteWordBackward()
	buffer.InsertChar("X")
	buffer.DeleteWordBackward()
	if buffer.Text() != "one two three " {
		t.Fatalf("kills = %q", buffer.Text())
	}
	buffer.Yank()
	if buffer.Text() != "one two three X" {
		t.Fatalf("yank = %q", buffer.Text())
	}
	buffer.YankPop()
	if buffer.Text() != "one two three four" {
		t.Fatalf("yank pop = %q", buffer.Text())
	}
	buffer.YankPop()
	single := newEditorBuffer()
	single.SetText("hello world")
	single.SetCursor(0, 5)
	single.DeleteWordBackward()
	single.Yank()
	before := single.Text()
	single.YankPop()
	if single.Text() != before {
		t.Fatalf("single-entry yank pop should not change")
	}
	multi := newEditorBuffer()
	multi.SetText("a\nb")
	multi.SetCursor(0, 1)
	multi.DeleteToLineEnd()
	multi.SetCursor(0, 0)
	multi.Yank()
	if !strings.Contains(multi.Text(), "\n") {
		t.Fatalf("multiline yank = %q", multi.Text())
	}
}

func TestDeleteYankedMultiline(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("first\nsecond\nthird")
	buffer.SetCursor(0, 5)
	buffer.DeleteToLineEnd()
	buffer.SetCursor(0, 0)
	buffer.Yank()
	buffer.YankPop()
}

func TestJumpToChar(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("abc def abc")
	buffer.SetCursor(0, 0)
	buffer.JumpToChar("d", true)
	if _, col := buffer.Cursor(); col != 4 {
		t.Fatalf("jump forward = %d", col)
	}
	buffer.JumpToChar("a", false)
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("jump backward = %d", col)
	}
	buffer.JumpToChar("zzz", true)
	buffer.JumpToChar("", true)
	buffer.SetText("one\ntwo\none")
	buffer.SetCursor(0, 0)
	buffer.JumpToChar("o", true)
	if line, _ := buffer.Cursor(); line != 1 {
		t.Fatalf("multiline jump = %d", line)
	}
	buffer.SetCursor(2, 0)
	buffer.JumpToChar("o", false)
	if line, _ := buffer.Cursor(); line != 1 {
		t.Fatalf("multiline backward = %d", line)
	}
}

func TestHandlePaste(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.HandlePaste("hello world")
	if buffer.Text() != "hello world" {
		t.Fatalf("paste = %q", buffer.Text())
	}
	buffer = newEditorBuffer()
	buffer.HandlePaste("line1\nline2\nline3")
	if buffer.Text() != "line1\nline2\nline3" {
		t.Fatalf("multiline paste = %q", buffer.Text())
	}
	buffer = newEditorBuffer()
	buffer.HandlePaste(strings.Repeat("x", 2000))
	if !strings.Contains(buffer.Text(), "[paste #1") {
		t.Fatalf("large paste should use marker: %q", buffer.Text()[:100])
	}
	if !strings.Contains(buffer.ExpandedText(), strings.Repeat("x", 100)) {
		t.Fatalf("expanded should contain content")
	}
	buffer = newEditorBuffer()
	buffer.SetText("abc")
	buffer.SetCursor(0, 3)
	buffer.HandlePaste("/tmp/path")
	if buffer.Text() != "abc /tmp/path" {
		t.Fatalf("path paste spacing = %q", buffer.Text())
	}
	buffer = newEditorBuffer()
	buffer.SetText("a\rb\rc")
	if buffer.Text() != "a\nb\nc" {
		t.Fatalf("normalize paste newlines = %q", buffer.Text())
	}
}

func TestExpandedTextWithoutMarkers(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("plain text")
	if buffer.ExpandedText() != "plain text" {
		t.Fatalf("expanded = %q", buffer.ExpandedText())
	}
}
