package interactive

import (
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

func (p *mdParser) atTableStart() bool {
	if p.pos+1 >= len(p.lines) {
		return false
	}
	header := p.lines[p.pos]
	separator := p.lines[p.pos+1]
	return strings.Contains(header, "|") && isTableSeparator(separator) && tableColumnCount(header) == tableColumnCount(separator)
}

func tableColumnCount(line string) int {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	if strings.TrimSpace(trimmed) == "" {
		return 0
	}
	return strings.Count(trimmed, "|") + 1
}

func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	if strings.TrimSpace(trimmed) == "" {
		return false
	}
	for _, cell := range strings.Split(trimmed, "|") {
		if !isTableSeparatorCell(cell) {
			return false
		}
	}
	return true
}

func isTableSeparatorCell(cell string) bool {
	trimmed := strings.TrimSpace(cell)
	if len(trimmed) < 3 {
		return false
	}
	left := strings.HasPrefix(trimmed, ":")
	right := strings.HasSuffix(trimmed, ":")
	if left {
		trimmed = trimmed[1:]
	}
	if right && strings.HasSuffix(trimmed, ":") {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) < 3 {
		return false
	}
	for _, char := range trimmed {
		if char != '-' {
			return false
		}
	}
	return true
}

func (p *mdParser) parseTable() mdBlock {
	header := parseTableRow(p.lines[p.pos])
	separator := p.lines[p.pos+1]
	block := mdBlock{kind: mdTable, header: header, align: parseTableAlignments(separator)}
	p.pos += 2
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "|") {
			break
		}
		block.rows = append(block.rows, parseTableRow(line))
		p.pos++
	}
	return block
}

func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	cells := strings.Split(trimmed, "|")
	result := make([]string, 0, len(cells))
	for _, cell := range cells {
		result = append(result, strings.TrimSpace(cell))
	}
	return result
}

func parseTableAlignments(separator string) []mdAlign {
	cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(separator), "|"), "|"), "|")
	aligns := make([]mdAlign, 0, len(cells))
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		switch {
		case strings.HasPrefix(trimmed, ":") && strings.HasSuffix(trimmed, ":"):
			aligns = append(aligns, mdAlignCenter)
		case strings.HasSuffix(trimmed, ":"):
			aligns = append(aligns, mdAlignRight)
		default:
			aligns = append(aligns, mdAlignLeft)
		}
	}
	return aligns
}

const tableMaxWordWidth = 30

func (r *mdRenderer) renderTable(block mdBlock, last bool) []string {
	columns := len(block.header)
	if columns == 0 {
		return nil
	}
	overhead := 3*columns + 1
	availableForCells := r.width - overhead
	if availableForCells < columns {
		return r.rawTableFallback(block, last)
	}
	natural, minWord := r.tableWidths(block, columns)
	minWidths := r.fitMinWidths(minWord, availableForCells, columns)
	widths := r.tableColumnWidths(natural, minWidths, availableForCells, columns, overhead)
	aligns := block.align
	lines := []string{tableBorder(widths, "┌", "┬", "┐")}
	headerLines := r.tableRowLines(block.header, widths, aligns, true)
	lines = append(lines, headerLines...)
	lines = append(lines, tableBorder(widths, "├", "┼", "┤"))
	for _, row := range block.rows {
		lines = append(lines, r.tableRowLines(row, widths, aligns, false)...)
	}
	lines = append(lines, tableBorder(widths, "└", "┴", "┘"))
	if !last {
		lines = append(lines, "")
	}
	return lines
}

func (r *mdRenderer) rawTableFallback(block mdBlock, last bool) []string {
	raw := tableSource(block)
	lines := tui.WrapTextWithANSI(raw, r.width)
	if !last {
		lines = append(lines, "")
	}
	return lines
}

func tableSource(block mdBlock) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(block.header, " | ") + " |\n")
	for _, row := range block.rows {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (r *mdRenderer) tableWidths(block mdBlock, columns int) ([]int, []int) {
	natural := make([]int, columns)
	minWord := make([]int, columns)
	measure := func(cells []string) {
		for i := 0; i < columns && i < len(cells); i++ {
			text := r.inline(cells[i], r.defaultContext())
			width := tui.VisibleWidth(text)
			if width > natural[i] {
				natural[i] = width
			}
			if longest := minInt(longestWordWidth(text), tableMaxWordWidth); longest > minWord[i] {
				minWord[i] = longest
			}
		}
	}
	measure(block.header)
	for _, row := range block.rows {
		measure(row)
	}
	for i := range natural {
		if natural[i] == 0 {
			natural[i] = 1
		}
		if minWord[i] == 0 {
			minWord[i] = 1
		}
	}
	return natural, minWord
}

func (r *mdRenderer) fitMinWidths(minWord []int, availableForCells, columns int) []int {
	minWidths := append([]int(nil), minWord...)
	total := 0
	for _, width := range minWidths {
		total += width
	}
	if total <= availableForCells {
		return minWidths
	}
	minWidths = make([]int, columns)
	remaining := availableForCells - columns
	if remaining <= 0 {
		for i := range minWidths {
			minWidths[i] = 1
		}
		return minWidths
	}
	totalWeight := 0
	for _, width := range minWord {
		totalWeight += maxInt(0, width-1)
	}
	allocated := 0
	if totalWeight > 0 {
		for i, width := range minWord {
			share := maxInt(0, width-1) * remaining / totalWeight
			minWidths[i] += share
			allocated += share
		}
	}
	for i := 0; allocated < remaining; i++ {
		minWidths[i%columns]++
		allocated++
	}
	return minWidths
}

func (r *mdRenderer) tableColumnWidths(natural, minWidths []int, availableForCells, columns, overhead int) []int {
	totalNatural := overhead
	for _, width := range natural {
		totalNatural += width
	}
	if totalNatural <= availableForCells+overhead {
		widths := make([]int, columns)
		for i := range widths {
			widths[i] = maxInt(natural[i], minWidths[i])
		}
		return widths
	}
	totalGrow := 0
	for i := range natural {
		totalGrow += maxInt(0, natural[i]-minWidths[i])
	}
	extra := maxInt(0, availableForCells-minSum(minWidths))
	widths := make([]int, columns)
	for i := range widths {
		delta := maxInt(0, natural[i]-minWidths[i])
		grow := 0
		if totalGrow > 0 {
			grow = delta * extra / totalGrow
		}
		widths[i] = minWidths[i] + grow
	}
	allocated := 0
	for _, width := range widths {
		allocated += width
	}
	remaining := availableForCells - allocated
	for i := 0; remaining > 0 && i < columns*columns+columns; i++ {
		index := i % columns
		if widths[index] < natural[index] {
			widths[index]++
			remaining--
		}
	}
	return widths
}

func minSum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func tableBorder(widths []int, left, middle, right string) string {
	cells := make([]string, 0, len(widths))
	for _, width := range widths {
		cells = append(cells, strings.Repeat("─", width))
	}
	return left + "─" + strings.Join(cells, "─"+middle+"─") + "─" + right
}

func (r *mdRenderer) tableRowLines(cells []string, widths []int, aligns []mdAlign, header bool) []string {
	rendered := make([][]string, len(widths))
	for i := range widths {
		text := ""
		if i < len(cells) {
			text = r.inline(cells[i], r.defaultContext())
		}
		rendered[i] = wrapTableCell(text, widths[i])
	}
	height := 0
	for _, cellLines := range rendered {
		if len(cellLines) > height {
			height = len(cellLines)
		}
	}
	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		parts := make([]string, 0, len(widths))
		for i, width := range widths {
			text := ""
			if row < len(rendered[i]) {
				text = rendered[i][row]
			}
			parts = append(parts, alignCell(text, width, aligns[i]))
		}
		joined := "│ " + strings.Join(parts, " │ ") + " │"
		if header {
			joined = r.theme.Bold(joined)
		}
		lines = append(lines, joined)
	}
	return lines
}

func wrapTableCell(text string, width int) []string {
	lines := tui.WrapTextWithANSI(text, maxInt(1, width))
	for i, line := range lines {
		if i < len(lines)-1 {
			lines[i] = line + "\x1b[22;23;24;25;27;28;29;39m"
		}
	}
	return lines
}

func alignCell(text string, width int, align mdAlign) string {
	gap := maxInt(0, width-tui.VisibleWidth(text))
	switch align {
	case mdAlignRight:
		return strings.Repeat(" ", gap) + text
	case mdAlignCenter:
		left := gap / 2
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", gap-left)
	default:
		return text + strings.Repeat(" ", gap)
	}
}

func longestWordWidth(text string) int {
	longest := 0
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '\t' }) {
		if width := tui.VisibleWidth(word); width > longest {
			longest = width
		}
	}
	return longest
}
