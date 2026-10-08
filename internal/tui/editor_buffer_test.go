package tui

import (
	"strings"
	"testing"
)

func TestNormalizeEditorText(t *testing.T) {
	if got := normalizeEditorText("a\r\nb\rc\td"); got != "a\nb\nc    d" {
		t.Fatalf("normalize = %q", got)
	}
}

func TestFilterPastedText(t *testing.T) {
	if got := filterPastedText("a\x01b\nc\x7fd"); got != "ab\ncd" {
		t.Fatalf("filter = %q", got)
	}
}

func TestFormatAndParsePasteMarker(t *testing.T) {
	marker := formatPasteMarker(3, 12, true)
	if marker != "[paste #3 +12 lines]" {
		t.Fatalf("marker = %q", marker)
	}
	marker = formatPasteMarker(2, 500, false)
	if marker != "[paste #2 500 chars]" {
		t.Fatalf("marker = %q", marker)
	}
	if id, ok := parsePasteMarkerID("[paste #12 +5 lines]"); !ok || id != 12 {
		t.Fatalf("parse = %d %v", id, ok)
	}
	if _, ok := parsePasteMarkerID("nope"); ok {
		t.Fatalf("should fail")
	}
	if _, ok := parsePasteMarkerID("[paste #]"); ok {
		t.Fatalf("empty id should fail")
	}
	if _, ok := parsePasteMarkerID("[paste #3 +5 lines"); ok {
		t.Fatalf("missing bracket should fail")
	}
}

func TestFindPasteMarkerSpans(t *testing.T) {
	line := "hello [paste #1 +3 lines] world [paste #2 10 chars] end"
	spans := findPasteMarkerSpans(line, nil)
	if len(spans) != 2 {
		t.Fatalf("spans = %v", spans)
	}
	valid := map[int]struct{}{1: {}}
	spans = findPasteMarkerSpans(line, valid)
	if len(spans) != 1 {
		t.Fatalf("filtered spans = %v", spans)
	}
	spans = findPasteMarkerSpans("no markers", nil)
	if len(spans) != 0 {
		t.Fatalf("empty spans = %v", spans)
	}
	spans = findPasteMarkerSpans("broken [paste #x]", nil)
	if len(spans) != 0 {
		t.Fatalf("broken marker spans = %v", spans)
	}
	spans = findPasteMarkerSpans("unterminated [paste #1", nil)
	if len(spans) != 0 {
		t.Fatalf("unterminated spans = %v", spans)
	}
}

func TestEditorBufferTextAndCursor(t *testing.T) {
	buffer := newEditorBuffer()
	if buffer.Text() != "" {
		t.Fatalf("empty text = %q", buffer.Text())
	}
	buffer.SetText("hello\nworld")
	if buffer.Text() != "hello\nworld" {
		t.Fatalf("text = %q", buffer.Text())
	}
	lines := buffer.Lines()
	if len(lines) != 2 || lines[0] != "hello" {
		t.Fatalf("lines = %q", lines)
	}
	line, col := buffer.Cursor()
	if line != 1 || col != 5 {
		t.Fatalf("cursor = %d %d", line, col)
	}
	buffer.SetCursor(0, 2)
	line, col = buffer.Cursor()
	if line != 0 || col != 2 {
		t.Fatalf("set cursor = %d %d", line, col)
	}
	buffer.SetCursor(-5, -5)
	line, col = buffer.Cursor()
	if line != 0 || col != 0 {
		t.Fatalf("clamp low = %d %d", line, col)
	}
	buffer.SetCursor(99, 99)
	line, col = buffer.Cursor()
	if line != 1 || col != 5 {
		t.Fatalf("clamp high = %d %d", line, col)
	}
	buffer.SetText("")
	if buffer.Text() != "" || len(buffer.Lines()) != 1 {
		t.Fatalf("clear text failed")
	}
}

func TestEditorBufferInsertAndNewline(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.InsertTextAtCursor("hello")
	if buffer.Text() != "hello" {
		t.Fatalf("insert = %q", buffer.Text())
	}
	buffer.InsertTextAtCursor("")
	if buffer.Text() != "hello" {
		t.Fatalf("empty insert changed text")
	}
	buffer.SetCursor(0, 5)
	buffer.InsertChar("!")
	if buffer.Text() != "hello!" {
		t.Fatalf("char insert = %q", buffer.Text())
	}
	buffer.InsertChar("")
	if buffer.Text() != "hello!" {
		t.Fatalf("empty char changed text")
	}
	buffer.SetCursor(0, 2)
	buffer.Newline()
	if buffer.Text() != "he\nllo!" {
		t.Fatalf("newline = %q", buffer.Text())
	}
	line, col := buffer.Cursor()
	if line != 1 || col != 0 {
		t.Fatalf("newline cursor = %d %d", line, col)
	}
	buffer.InsertTextAtCursor("X\nY\nZ")
	if !strings.Contains(buffer.Text(), "X\nY\nZ") {
		t.Fatalf("multiline insert = %q", buffer.Text())
	}
}

func TestEditorBufferCharMovement(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("ab\ncd")
	buffer.SetCursor(0, 0)
	buffer.MoveRight()
	if _, col := buffer.Cursor(); col != 1 {
		t.Fatalf("right col = %d", col)
	}
	buffer.MoveLeft()
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("left col = %d", col)
	}
	buffer.MoveLeft()
	if line, _ := buffer.Cursor(); line != 0 {
		t.Fatalf("left at start should stay")
	}
	buffer.SetCursor(0, 2)
	buffer.MoveRight()
	if line, col := buffer.Cursor(); line != 1 || col != 0 {
		t.Fatalf("wrap right = %d %d", line, col)
	}
	buffer.MoveLeft()
	if line, col := buffer.Cursor(); line != 0 || col != 2 {
		t.Fatalf("wrap left = %d %d", line, col)
	}
	buffer.SetCursor(1, 2)
	buffer.MoveRight()
	if line, col := buffer.Cursor(); line != 1 || col != 2 {
		t.Fatalf("right at end should stay")
	}
	buffer.SetText("héllo")
	buffer.SetCursor(0, 0)
	buffer.MoveRight()
	if _, col := buffer.Cursor(); col <= 0 {
		t.Fatalf("grapheme right failed")
	}
	buffer.MoveLeft()
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("grapheme left failed")
	}
}

func TestEditorBufferMarkerMovement(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.HandlePaste(strings.Repeat("x\n", 12))
	text := buffer.Text()
	if !strings.Contains(text, "[paste #1") {
		t.Fatalf("expected marker, got %q", text)
	}
	buffer.SetCursor(0, 0)
	buffer.MoveRight()
	if _, col := buffer.Cursor(); col == 0 {
		t.Fatalf("marker right should jump")
	}
	_, col := buffer.Cursor()
	line := buffer.Lines()[0]
	if col != len(line) {
		t.Fatalf("marker right col = %d want %d", col, len(line))
	}
	buffer.MoveLeft()
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("marker left col = %d", col)
	}
}

func TestEditorBufferWordMovement(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("alpha beta gamma")
	buffer.SetCursor(0, 16)
	buffer.MoveWordLeft()
	if _, col := buffer.Cursor(); col != 11 {
		t.Fatalf("word left = %d", col)
	}
	buffer.MoveWordRight()
	if _, col := buffer.Cursor(); col != 16 {
		t.Fatalf("word right = %d", col)
	}
	buffer.SetCursor(0, 0)
	buffer.MoveWordLeft()
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("word left at start")
	}
	buffer.SetCursor(0, 16)
	buffer.MoveWordRight()
	if _, col := buffer.Cursor(); col != 16 {
		t.Fatalf("word right at end")
	}
	buffer.SetText("one\ntwo")
	buffer.SetCursor(1, 0)
	buffer.MoveWordLeft()
	if line, _ := buffer.Cursor(); line != 0 {
		t.Fatalf("word left across lines")
	}
	buffer.SetCursor(0, 3)
	buffer.MoveWordRight()
	if line, col := buffer.Cursor(); line != 1 || col != 0 {
		t.Fatalf("word right across lines = %d %d", line, col)
	}
}

func TestEditorBufferLineAndBufferMovement(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("one\ntwo\nthree")
	buffer.SetCursor(1, 1)
	buffer.MoveLineStart()
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("line start")
	}
	buffer.MoveLineEnd()
	if _, col := buffer.Cursor(); col != 3 {
		t.Fatalf("line end")
	}
	buffer.MoveBufferStart()
	if line, col := buffer.Cursor(); line != 0 || col != 0 {
		t.Fatalf("buffer start")
	}
	buffer.MoveBufferEnd()
	if line, col := buffer.Cursor(); line != 2 || col != 5 {
		t.Fatalf("buffer end")
	}
}

func TestEditorBufferVisualMovement(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("abcdefghijklmnopqrstuvwxyz")
	visuals := buffer.buildVisualLines(10)
	if len(visuals) != 3 {
		t.Fatalf("visuals = %d", len(visuals))
	}
	buffer.SetCursor(0, 0)
	buffer.MoveVisualDown(10)
	line, col := buffer.Cursor()
	if line != 0 || col == 0 {
		t.Fatalf("visual down = %d %d", line, col)
	}
	buffer.MoveVisualUp(10)
	if _, col := buffer.Cursor(); col != 0 {
		t.Fatalf("visual up should return to start, col=%d", col)
	}
	buffer.MovePageDown(10, 5)
	buffer.MovePageUp(10, 5)
	buffer.MovePageDown(10, 0)
	buffer.MovePageUp(10, 0)
	if idx := buffer.findVisualAt(visuals, 0, 9999); idx < 0 {
		t.Fatalf("find visual overflow")
	}
	empty := newEditorBuffer()
	empty.buildVisualLines(0)
	empty.moveVisual(1, 0)
	if got := visualColumnOf("hello", 0, 2); got != 2 {
		t.Fatalf("visual col = %d", got)
	}
	if got := visualColumnOf("hello", 5, 2); got != 0 {
		t.Fatalf("visual col reversed = %d", got)
	}
	if got := byteOffsetForVisualCol("hello", 0, 2); got != 2 {
		t.Fatalf("byte offset = %d", got)
	}
	if got := byteOffsetForVisualCol("hello", 0, 0); got != 0 {
		t.Fatalf("zero offset = %d", got)
	}
}

func TestEditorBufferVisualSticky(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.SetText("short\na much longer line here\nmid")
	buffer.SetCursor(1, 10)
	buffer.MoveVisualUp(80)
	buffer.MoveVisualDown(80)
	if _, col := buffer.Cursor(); col == 0 {
		t.Fatalf("sticky should preserve column")
	}
}

func TestEditorBufferCrop(t *testing.T) {
	buffer := newEditorBuffer()
	long := strings.Repeat("x", 5000)
	buffer.SetText(long)
	cropped := buffer.cropForLayout(long)
	if VisibleWidth(cropped) > editorMaxLineCols {
		t.Fatalf("crop failed: %d", VisibleWidth(cropped))
	}
	visuals := buffer.buildVisualLines(80)
	if len(visuals) == 0 {
		t.Fatalf("no visuals for cropped line")
	}
	if got := buffer.cropForLayout("short"); got != "short" {
		t.Fatalf("short should not crop")
	}
}

func TestEditorBufferUndo(t *testing.T) {
	buffer := newEditorBuffer()
	buffer.Undo()
	buffer.SetText("hello")
	buffer.InsertChar("!")
	if buffer.Text() != "hello!" {
		t.Fatalf("text = %q", buffer.Text())
	}
	buffer.Undo()
	if buffer.Text() != "hello" {
		t.Fatalf("undo = %q", buffer.Text())
	}
	buffer.Undo()
	if buffer.Text() != "" {
		t.Fatalf("undo to empty = %q", buffer.Text())
	}
	buffer.Undo()
}
