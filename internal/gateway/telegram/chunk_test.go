package telegram

import (
	"strings"
	"testing"
)

func TestChunkTextShortSingleChunk(t *testing.T) {
	got := chunkText("hello world", legacyChunkMax)
	if len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("chunks = %q", got)
	}
}

func TestChunkTextExactMaxSingleChunk(t *testing.T) {
	text := strings.Repeat("a", legacyChunkMax)
	got := chunkText(text, legacyChunkMax)
	if len(got) != 1 || got[0] != text {
		t.Fatalf("chunks = %q", got)
	}
}

func TestChunkTextSpaceBoundary(t *testing.T) {
	text := strings.Repeat("a", 4000) + " " + strings.Repeat("b", 200)
	got := chunkText(text, 4096)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if got[0] != strings.Repeat("a", 4000) {
		t.Fatalf("chunk0 = %q", got[0])
	}
	if got[1] != strings.Repeat("b", 200) {
		t.Fatalf("chunk1 = %q", got[1])
	}
}

func TestChunkTextParagraphBoundary(t *testing.T) {
	text := "aaaa\n\nbbbb"
	got := chunkText(text, 8)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if got[0] != "aaaa\n" {
		t.Fatalf("chunk0 = %q", got[0])
	}
	if got[1] != "bbbb" {
		t.Fatalf("chunk1 = %q", got[1])
	}
}

func TestChunkTextNewlineBoundary(t *testing.T) {
	text := strings.Repeat("a", 4000) + "\n" + strings.Repeat("b", 200)
	got := chunkText(text, 4096)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if got[0] != strings.Repeat("a", 4000) || got[1] != strings.Repeat("b", 200) {
		t.Fatalf("chunks = %q", got)
	}
}

func TestChunkTextHardCutNoBoundary(t *testing.T) {
	text := strings.Repeat("x", 5000)
	got := chunkText(text, 4096)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if got[0] != strings.Repeat("x", 4096) || got[1] != strings.Repeat("x", 904) {
		t.Fatalf("chunks = %q", got)
	}
}

func TestChunkTextMultibyteRunes(t *testing.T) {
	text := strings.Repeat("é", 5000)
	got := chunkText(text, 4096)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if len([]rune(got[0])) != 4096 || len([]rune(got[1])) != 904 {
		t.Fatalf("chunk rune lengths = %d %d", len([]rune(got[0])), len([]rune(got[1])))
	}
	if got[0]+got[1] != text {
		t.Fatalf("chunks do not reconstruct text")
	}
}

func TestChunkTextDefaultMax(t *testing.T) {
	text := strings.Repeat("a", 5000)
	got := chunkText(text, 0)
	if len(got) != 2 || len([]rune(got[0])) != legacyChunkMax {
		t.Fatalf("chunks = %q", got)
	}
}

func TestChunkTextTrimsLeadingWhitespace(t *testing.T) {
	text := strings.Repeat("a", 4000) + " \n " + strings.Repeat("b", 100)
	got := chunkText(text, 4096)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if got[1] != strings.Repeat("b", 100) {
		t.Fatalf("chunk1 = %q", got[1])
	}
}

func TestChunkTextBoundaryAtExactCut(t *testing.T) {
	text := strings.Repeat("a", 4095) + " " + strings.Repeat("b", 100)
	got := chunkText(text, 4096)
	if len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	if got[0] != strings.Repeat("a", 4095) || got[1] != strings.Repeat("b", 100) {
		t.Fatalf("chunks = %q", got)
	}
}

func TestDraftPartsSingleParagraph(t *testing.T) {
	if got := draftParts("only one paragraph", 4); got != nil {
		t.Fatalf("draftParts = %q", got)
	}
}

func TestDraftPartsTwoParagraphs(t *testing.T) {
	got := draftParts("p1\n\np2", 4)
	if len(got) != 1 || got[0] != "p1" {
		t.Fatalf("draftParts = %q", got)
	}
}

func TestDraftPartsMaxParts(t *testing.T) {
	text := "p1\n\np2\n\np3\n\np4\n\np5"
	got := draftParts(text, 4)
	want := []string{"p1", "p1\n\np2", "p1\n\np2\n\np3"}
	if len(got) != len(want) {
		t.Fatalf("draftParts = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("draftParts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDraftPartsMaxPartsLow(t *testing.T) {
	if got := draftParts("p1\n\np2", 1); got != nil {
		t.Fatalf("draftParts = %q", got)
	}
}

func TestDraftPartsSkipsEmptyParagraphs(t *testing.T) {
	got := draftParts("p1\n\n\n\np2", 4)
	if len(got) != 1 || got[0] != "p1" {
		t.Fatalf("draftParts = %q", got)
	}
}

func TestDraftIDForStableNonZero(t *testing.T) {
	a := draftIDFor(123, "1:100")
	b := draftIDFor(123, "1:100")
	if a == 0 || a != b {
		t.Fatalf("draft ids = %d %d", a, b)
	}
	if c := draftIDFor(124, "1:100"); c == a {
		t.Fatalf("different chat produced same id %d", c)
	}
	if d := draftIDFor(123, "1:101"); d == a {
		t.Fatalf("different delivery produced same id %d", d)
	}
}
