package interactive

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestRenderMermaidFlowchartAccepted(t *testing.T) {
	theme := mustTheme(t)
	source := "graph TD\n  A[Start] --> B{Decide}\n  B -->|yes| C(Done)\n  B -->|no| D[Other]\n"
	if _, warning, ok := RenderMermaid(source, 80, theme); !ok {
		t.Fatalf("acyclic flowchart rejected: %s", warning)
	}
	source = "flowchart LR\n  A[Start] --> B[Next]\n"
	lines, warning, ok := RenderMermaid(source, 80, theme)
	if !ok {
		t.Fatalf("LR flowchart rejected: %s", warning)
	}
	joined := strings.Join(stripLines(lines), "\n")
	if !strings.Contains(joined, "Start") || !strings.Contains(joined, "Next") {
		t.Fatalf("labels missing: %q", joined)
	}
}

func TestRenderMermaidDirections(t *testing.T) {
	theme := mustTheme(t)
	for _, direction := range []string{"TD", "TB", "LR", "RL", "BT"} {
		source := "graph " + direction + "\n  A[One] --> B[Two]\n"
		if _, warning, ok := RenderMermaid(source, 80, theme); !ok {
			t.Fatalf("direction %s rejected: %s", direction, warning)
		}
	}
}

func TestRenderMermaidRejections(t *testing.T) {
	theme := mustTheme(t)
	cases := []struct {
		name   string
		source string
	}{
		{"cycle", "graph TD\n  A --> B\n  B --> A\n"},
		{"subgraph", "graph TD\n  subgraph one\n  A --> B\n  end\n"},
		{"html", "graph TD\n  A[<b>bold</b>] --> B\n"},
		{"style", "graph TD\n  A --> B\n  style A fill:#f00\n"},
		{"click", "graph TD\n  A --> B\n  click A callback\n"},
		{"directive", "%%{init: {'theme':'dark'}}%%\ngraph TD\n  A --> B\n"},
		{"unknownType", "pie\n  \"a\": 1\n"},
		{"missingDirection", "graph\n  A --> B\n"},
		{"unknownShape", "graph TD\n  A~Start~ --> B\n"},
		{"unsupportedStatement", "graph TD\n  A --> B\n  unknown thing here\n"},
		{"badEdge", "graph TD\n  --> B\n"},
	}
	for _, testCase := range cases {
		if _, warning, ok := RenderMermaid(testCase.source, 80, theme); ok || warning == "" {
			t.Fatalf("%s should be rejected with a warning", testCase.name)
		}
	}
}

func TestRenderMermaidBudget(t *testing.T) {
	theme := mustTheme(t)
	var builder strings.Builder
	builder.WriteString("graph TD\n")
	for index := 0; index < mermaidMaxNodes+2; index++ {
		builder.WriteString("  ")
		builder.WriteString(string(rune('A' + index%26)))
		builder.WriteString(itoa(index))
		builder.WriteString("[node] --> next\n")
	}
	if _, warning, ok := RenderMermaid(builder.String(), 80, theme); ok || warning == "" {
		t.Fatal("over-budget flowchart should be rejected")
	}
	oversized := "graph TD\n  A[Start] --> B[End]\n" + strings.Repeat("%% padding padding padding\n", 400)
	if _, warning, ok := RenderMermaid(oversized, 80, theme); ok || warning == "" {
		t.Fatal("oversized diagram should be rejected")
	}
}

func TestRenderMermaidWidthAmbiguity(t *testing.T) {
	theme := mustTheme(t)
	source := "graph TD\n  A[A very long label that will not fit] --> B[End]\n"
	if _, warning, ok := RenderMermaid(source, 8, theme); ok || warning == "" {
		t.Fatal("diagram wider than the viewport should warn")
	}
}

func TestRenderMermaidSequenceAccepted(t *testing.T) {
	theme := mustTheme(t)
	source := "sequenceDiagram\n  participant A as Alice\n  participant B as Bob\n  A->>B: hello\n  B-->>A: hi\n"
	lines, warning, ok := RenderMermaid(source, 80, theme)
	if !ok {
		t.Fatalf("sequence rejected: %s", warning)
	}
	joined := strings.Join(stripLines(lines), "\n")
	if !strings.Contains(joined, "Alice") || !strings.Contains(joined, "hello") || !strings.Contains(joined, "Bob") {
		t.Fatalf("sequence output incomplete: %q", joined)
	}
}

func TestRenderMermaidSequenceRejections(t *testing.T) {
	theme := mustTheme(t)
	if _, warning, ok := RenderMermaid("sequenceDiagram\n  Note over A: hi\n", 80, theme); ok || warning == "" {
		t.Fatal("unsupported sequence statement should warn")
	}
	if _, warning, ok := RenderMermaid("sequenceDiagram\n  A->>B: hello\n", 80, theme); !ok {
		t.Fatalf("implicit participants should be registered: %s", warning)
	}
}

func TestMermaidMarkdownIntegration(t *testing.T) {
	theme := mustTheme(t)
	accepted := NewMarkdown("```mermaid\ngraph TD\n  A[Start] --> B[End]\n```\n", 0, 0, theme, MarkdownStyle{}, false)
	rendered := strings.Join(stripLines(accepted.Render(80)), "\n")
	if !strings.Contains(rendered, "Start") || !strings.Contains(rendered, "End") {
		t.Fatalf("mermaid box art missing: %q", rendered)
	}
	if strings.Contains(rendered, "```mermaid") {
		t.Fatalf("accepted mermaid should replace the fence: %q", rendered)
	}
	rejected := NewMarkdown("```mermaid\ngraph TD\n  A --> B\n  B --> A\n```\n", 0, 0, theme, MarkdownStyle{}, false)
	rejectedRendered := strings.Join(stripLines(rejected.Render(80)), "\n")
	if !strings.Contains(rejectedRendered, "```mermaid") {
		t.Fatalf("rejected mermaid should preserve the fence: %q", rejectedRendered)
	}
	if !strings.Contains(rejectedRendered, "mermaid:") {
		t.Fatalf("rejected mermaid should carry a warning: %q", rejectedRendered)
	}
	_ = tui.VisibleWidth
}

func TestRenderMermaidRepeatedDefinitions(t *testing.T) {
	theme := mustTheme(t)
	same := "graph TD\n  A[Start] --> B[End]\n  A[Start] --> B[End]\n"
	if _, warning, ok := RenderMermaid(same, 80, theme); !ok {
		t.Fatalf("same repeated definition rejected: %s", warning)
	}
	upgrade := "graph TD\n  A --> B\n  A[Start] --> B[End]\n"
	lines, warning, ok := RenderMermaid(upgrade, 80, theme)
	if !ok {
		t.Fatalf("bare then shaped rejected: %s", warning)
	}
	joined := strings.Join(stripLines(lines), "\n")
	if !strings.Contains(joined, "Start") || !strings.Contains(joined, "End") {
		t.Fatalf("upgraded labels missing: %q", joined)
	}
	confirm := "graph TD\n  A[Start] --> B[End]\n  A --> B\n"
	if _, warning, ok := RenderMermaid(confirm, 80, theme); !ok {
		t.Fatalf("bare confirm rejected: %s", warning)
	}
	conflicts := []struct {
		name   string
		source string
	}{
		{"label", "graph TD\n  A[One] --> B[End]\n  A[Two] --> B[End]\n"},
		{"shape", "graph TD\n  A[One] --> B[End]\n  A(One) --> B[End]\n"},
		{"targetLabel", "graph TD\n  A[Start] --> B[One]\n  A[Start] --> B[Two]\n"},
		{"targetShape", "graph TD\n  A[Start] --> B[One]\n  A[Start] --> B(One)\n"},
	}
	for _, testCase := range conflicts {
		if _, warning, ok := RenderMermaid(testCase.source, 80, theme); ok || warning == "" {
			t.Fatalf("%s conflict should reject whole diagram", testCase.name)
		}
	}
	atomic := "graph TD\n  A[Start] --> B[End]\n  A[Other] --> B[End]\n"
	if lines, _, ok := RenderMermaid(atomic, 80, theme); ok || len(lines) != 0 {
		t.Fatalf("conflicting diagram must be atomic, got ok=%v lines=%d", ok, len(lines))
	}
}

func TestRenderMermaidStrictArrows(t *testing.T) {
	theme := mustTheme(t)
	accepted := []struct {
		name   string
		source string
	}{
		{"thin", "graph TD\n  A --> B\n"},
		{"thick", "graph TD\n  A ==> B\n"},
		{"pipeThin", "graph TD\n  A -->|yes| B\n"},
		{"pipeThick", "graph TD\n  A ==>|yes| B\n"},
		{"textLabel", "graph TD\n  A -- yes --> B\n"},
		{"chain", "graph TD\n  A --> B --> C\n"},
		{"single", "graph TD\n  A[Alone]\n"},
	}
	for _, testCase := range accepted {
		if _, warning, ok := RenderMermaid(testCase.source, 80, theme); !ok {
			t.Fatalf("%s should be accepted: %s", testCase.name, warning)
		}
	}
	rejected := []struct {
		name   string
		source string
	}{
		{"undirected", "graph TD\n  A --- B\n"},
		{"undirectedSpaced", "graph TD\n  A --- B\n  B --> C\n"},
		{"dotted", "graph TD\n  A -.-> B\n"},
		{"circle", "graph TD\n  A --o B\n"},
		{"circleRev", "graph TD\n  A o--o B\n"},
		{"cross", "graph TD\n  A --x B\n"},
		{"crossRev", "graph TD\n  A x--x B\n"},
		{"incompleteTrailing", "graph TD\n  A -->\n"},
		{"incompleteChain", "graph TD\n  A --> B -->\n"},
		{"incompleteLeading", "graph TD\n  --> B\n"},
		{"incompleteDashes", "graph TD\n  A --\n"},
		{"bidirectional", "graph TD\n  A <--> B\n"},
		{"thickUndirected", "graph TD\n  A === B\n"},
		{"unclosedPipe", "graph TD\n  A -->|yes B\n"},
	}
	for _, testCase := range rejected {
		if _, warning, ok := RenderMermaid(testCase.source, 80, theme); ok || warning == "" {
			t.Fatalf("%s should be rejected", testCase.name)
		}
	}
}

func TestRenderMermaidDirectionGoldens(t *testing.T) {
	theme := mustTheme(t)
	vertical := []string{"TD", "TB"}
	for _, direction := range vertical {
		source := "graph " + direction + "\n  A[One] --> B[Two]\n"
		lines, warning, ok := RenderMermaid(source, 80, theme)
		if !ok {
			t.Fatalf("%s rejected: %s", direction, warning)
		}
		joined := strings.Join(stripLines(lines), "\n")
		if !strings.Contains(joined, "One") || !strings.Contains(joined, "Two") || !strings.Contains(joined, "Two") {
			t.Fatalf("%s topology missing: %q", direction, joined)
		}
		if !strings.Contains(joined, "▼") {
			t.Fatalf("%s should use down arrow: %q", direction, joined)
		}
		if strings.Contains(joined, "▶") || strings.Contains(joined, "◀") || strings.Contains(joined, "▲") {
			t.Fatalf("%s should not use horizontal or up arrow: %q", direction, joined)
		}
		onePos := strings.Index(joined, "One")
		twoPos := strings.Index(joined, "Two")
		if onePos < 0 || twoPos < 0 || onePos > twoPos {
			t.Fatalf("%s order should be top-down: %q", direction, joined)
		}
	}
	source := "graph BT\n  A[One] --> B[Two]\n"
	lines, warning, ok := RenderMermaid(source, 80, theme)
	if !ok {
		t.Fatalf("BT rejected: %s", warning)
	}
	joined := strings.Join(stripLines(lines), "\n")
	if !strings.Contains(joined, "▲") {
		t.Fatalf("BT should use up arrow: %q", joined)
	}
	if !strings.Contains(joined, "One") || !strings.Contains(joined, "Two") {
		t.Fatalf("BT topology missing: %q", joined)
	}
	if strings.Index(joined, "Two") > strings.Index(joined, "One") {
		t.Fatalf("BT should place target above source: %q", joined)
	}
	for _, direction := range []string{"LR", "RL"} {
		source := "graph " + direction + "\n  A[One] --> B[Two]\n"
		lines, warning, ok := RenderMermaid(source, 80, theme)
		if !ok {
			t.Fatalf("%s rejected: %s", direction, warning)
		}
		plain := stripLines(lines)
		joined := strings.Join(plain, "\n")
		if !strings.Contains(joined, "One") || !strings.Contains(joined, "Two") {
			t.Fatalf("%s topology missing: %q", direction, joined)
		}
		foundHorizontal := false
		for _, line := range plain {
			if strings.Contains(line, "One") && strings.Contains(line, "Two") {
				foundHorizontal = true
			}
		}
		if len(plain) >= 3 {
			if strings.Contains(plain[0], "┌") && strings.Contains(plain[1], "One") && strings.Contains(plain[1], "Two") {
				foundHorizontal = true
			}
		}
		if !foundHorizontal {
			t.Fatalf("%s should place boxes horizontally: %q", direction, joined)
		}
	}
	lrLines, _, _ := RenderMermaid("graph LR\n  A[One] --> B[Two]\n", 80, theme)
	rlLines, _, _ := RenderMermaid("graph RL\n  A[One] --> B[Two]\n", 80, theme)
	lrJoined := strings.Join(stripLines(lrLines), "\n")
	rlJoined := strings.Join(stripLines(rlLines), "\n")
	if !strings.Contains(lrJoined, "▶") && !strings.Contains(lrJoined, "──▶") {
		t.Fatalf("LR should use right arrow: %q", lrJoined)
	}
	if !strings.Contains(rlJoined, "◀") {
		t.Fatalf("RL should use left arrow: %q", rlJoined)
	}
	if !strings.Contains(rlJoined, "One") || !strings.Contains(rlJoined, "Two") {
		t.Fatalf("RL topology missing: %q", rlJoined)
	}
}

func TestRenderMermaidTopologyShapesLabels(t *testing.T) {
	theme := mustTheme(t)
	source := "graph TD\n  A[Start] --> B{Decide}\n  B -->|yes| C(Done)\n  B -->|no| D[Other]\n"
	lines, warning, ok := RenderMermaid(source, 80, theme)
	if !ok {
		t.Fatalf("branch rejected: %s", warning)
	}
	joined := strings.Join(stripLines(lines), "\n")
	for _, want := range []string{"Start", "Decide", "Done", "Other", "yes", "no"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("topology missing %q in %q", want, joined)
		}
	}
	if !strings.Contains(joined, "╭") || !strings.Contains(joined, "╱") || !strings.Contains(joined, "┌") {
		t.Fatalf("shapes not preserved: %q", joined)
	}
	chain := "graph TD\n  A[One] --> B[Two]\n  B[Two] --> C[Three]\n"
	chainLines, warning, ok := RenderMermaid(chain, 80, theme)
	if !ok {
		t.Fatalf("chain rejected: %s", warning)
	}
	chainJoined := strings.Join(stripLines(chainLines), "\n")
	if strings.Index(chainJoined, "One") > strings.Index(chainJoined, "Two") || strings.Index(chainJoined, "Two") > strings.Index(chainJoined, "Three") {
		t.Fatalf("chain order broken: %q", chainJoined)
	}
	horizontal := "graph LR\n  A[Start] -->|yes| B[Next]\n"
	horizontalLines, warning, ok := RenderMermaid(horizontal, 80, theme)
	if !ok {
		t.Fatalf("horizontal rejected: %s", warning)
	}
	horizontalJoined := strings.Join(stripLines(horizontalLines), "\n")
	if !strings.Contains(horizontalJoined, "Start") || !strings.Contains(horizontalJoined, "Next") || !strings.Contains(horizontalJoined, "yes") {
		t.Fatalf("horizontal topology missing: %q", horizontalJoined)
	}
}

func TestRenderMermaidWidthBudgets(t *testing.T) {
	theme := mustTheme(t)
	narrow := "graph TD\n  A[A very long label that will not fit] --> B[End]\n"
	if _, warning, ok := RenderMermaid(narrow, 8, theme); ok || warning == "" {
		t.Fatal("narrow vertical should warn")
	}
	wide := "graph LR\n  A[Start] --> B[Next]\n  B --> C[Third]\n  C --> D[Fourth]\n"
	if _, warning, ok := RenderMermaid(wide, 20, theme); ok || warning == "" {
		t.Fatal("narrow horizontal should warn")
	}
	sequence := "sequenceDiagram\n  participant A as Alice\n  participant B as Bob\n  A->>B: hello\n"
	if _, warning, ok := RenderMermaid(sequence, 10, theme); ok || warning == "" {
		t.Fatal("narrow sequence should warn")
	}
	if _, warning, ok := RenderMermaid("graph TD\n  A --> B\n", 0, theme); ok || warning == "" {
		t.Fatal("zero width should warn")
	}
}

func TestRenderMermaidCycleFallbackExact(t *testing.T) {
	theme := mustTheme(t)
	if _, warning, ok := RenderMermaid("graph TD\n  A --> B\n  B --> C\n  C --> A\n", 80, theme); ok || warning == "" {
		t.Fatal("cycle should be rejected")
	}
	if _, warning, ok := RenderMermaid("graph TD\n  A --> A\n", 80, theme); ok || warning == "" {
		t.Fatal("self cycle should be rejected")
	}
	source := "```mermaid\ngraph TD\n  A --> B\n  B --> A\n```\n"
	block := NewMarkdown(source, 0, 0, theme, MarkdownStyle{}, false)
	rendered := stripLines(block.Render(80))
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "```mermaid") || !strings.Contains(joined, "```") {
		t.Fatalf("fallback should preserve fences: %q", joined)
	}
	if !strings.Contains(joined, "A --> B") || !strings.Contains(joined, "B --> A") {
		t.Fatalf("fallback should preserve full source: %q", joined)
	}
	if count := strings.Count(joined, "mermaid:"); count != 1 {
		t.Fatalf("fallback should carry exactly one warning, got %d in %q", count, joined)
	}
	widthSource := "```mermaid\ngraph TD\n  A[A very long label that will not fit] --> B[End]\n```\n"
	widthBlock := NewMarkdown(widthSource, 0, 0, theme, MarkdownStyle{}, false)
	widthRendered := strings.Join(stripLines(widthBlock.Render(8)), "\n")
	if count := strings.Count(widthRendered, "mermaid:"); count != 1 {
		t.Fatalf("width fallback should carry exactly one warning, got %d", count)
	}
}

func TestRenderMermaidSequenceStrict(t *testing.T) {
	theme := mustTheme(t)
	for _, arrow := range []string{"->", "-->", "->>", "-->>"} {
		source := "sequenceDiagram\n  A" + arrow + "B: hello\n"
		if _, warning, ok := RenderMermaid(source, 80, theme); !ok {
			t.Fatalf("%s should be accepted: %s", arrow, warning)
		}
	}
	rejected := []string{
		"sequenceDiagram\n  A-xB: hello\n",
		"sequenceDiagram\n  A--xB: hello\n",
		"sequenceDiagram\n  A->+B: hello\n",
		"sequenceDiagram\n  A--)B: hello\n",
		"sequenceDiagram\n  A->B hello\n",
		"sequenceDiagram\n  A->>>B: hello\n",
		"sequenceDiagram\n  A=>B: hello\n",
		"sequenceDiagram\n  Note over A: hi\n",
		"sequenceDiagram\n  loop test\n  A->>B: hi\n  end\n",
	}
	for _, source := range rejected {
		if _, warning, ok := RenderMermaid(source, 80, theme); ok || warning == "" {
			t.Fatalf("sequence variant should be rejected: %q", source)
		}
	}
	aliasSource := "sequenceDiagram\n  participant A as Alice\n  participant B as Bob\n  A->>B: hello\n  B-->>A: hi\n"
	lines, warning, ok := RenderMermaid(aliasSource, 80, theme)
	if !ok {
		t.Fatalf("alias rejected: %s", warning)
	}
	joined := strings.Join(stripLines(lines), "\n")
	if !strings.Contains(joined, "Alice") || !strings.Contains(joined, "Bob") {
		t.Fatalf("aliases not preserved: %q", joined)
	}
	if strings.Index(joined, "Alice") > strings.Index(joined, "hello") {
		t.Fatalf("message direction broken: %q", joined)
	}
	if _, warning, ok := RenderMermaid("sequenceDiagram\n  A->>B: hello\n", 80, theme); !ok {
		t.Fatalf("implicit participants rejected: %s", warning)
	}
}

func TestRenderMermaidFuzzBounded(t *testing.T) {
	theme := mustTheme(t)
	fragments := []string{
		"graph TD\n", "flowchart LR\n", "sequenceDiagram\n", "A", "B", "C",
		"[", "]", "(", ")", "{", "}", "\"", "'", "|", "-", ">", "=", ".", "<", "/",
		"-->", "==>", "---", "-.->", "--o", "--x", "->>", "-->>", "->", "-->",
		" ", "\n", "%%", "%%{", "subgraph", "style", "click", "end", "participant ", " as ", ":", "Note ",
	}
	for iteration := 0; iteration < 500; iteration++ {
		var builder strings.Builder
		parts := 1 + iteration%8
		for index := 0; index < parts; index++ {
			builder.WriteString(fragments[(iteration*7+index*13)%len(fragments)])
		}
		source := builder.String()
		if len(source) > mermaidMaxSource {
			continue
		}
		done := make(chan struct{})
		go func(input string) {
			_, _, _ = RenderMermaid(input, 80, theme)
			_, _, _ = RenderMermaid(input, 8, theme)
			close(done)
		}(source)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d did not terminate", iteration)
		}
	}
	long := "graph TD\n" + strings.Repeat("  A --> B\n", 60)
	if _, warning, ok := RenderMermaid(long, 80, theme); ok || warning == "" {
		t.Fatal("over budget should be rejected")
	}
	many := "sequenceDiagram\n" + strings.Repeat("  A->>B: hi\n", 60)
	if _, warning, ok := RenderMermaid(many, 80, theme); ok || warning == "" {
		t.Fatal("over message budget should be rejected")
	}
}

func TestRenderMermaidConcurrentRace(t *testing.T) {
	theme := mustTheme(t)
	sources := []string{
		"graph TD\n  A[Start] --> B[End]\n",
		"graph LR\n  A[One] --> B[Two]\n",
		"graph BT\n  A[One] --> B[Two]\n",
		"sequenceDiagram\n  participant A as Alice\n  A->>B: hi\n",
		"graph TD\n  A --> B\n  B --> A\n",
	}
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, _, _ = RenderMermaid(sources[i%len(sources)], 80, theme)
			block := NewMarkdown("```mermaid\n"+sources[i%len(sources)]+"```\n", 0, 0, theme, MarkdownStyle{}, false)
			_ = block.Render(80)
		}(index)
	}
	group.Wait()
}
