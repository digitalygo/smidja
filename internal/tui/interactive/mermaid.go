package interactive

import (
	"sort"
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	mermaidMaxSource       = 8192
	mermaidMaxNodes        = 24
	mermaidMaxEdges        = 40
	mermaidMaxParticipants = 12
	mermaidMaxMessages     = 40
	mermaidMaxLabel        = 40
)

type mermaidNode struct {
	id    string
	label string
	shape byte
	order int
}

type mermaidEdge struct {
	from  string
	to    string
	label string
}

type mermaidMessage struct {
	from   string
	to     string
	label  string
	dashed bool
}

type mermaidDiagram struct {
	direction    string
	nodes        []mermaidNode
	edges        []mermaidEdge
	sequence     bool
	participants []string
	aliases      map[string]string
	messages     []mermaidMessage
}

func RenderMermaid(source string, width int, theme *tui.Theme) ([]string, string, bool) {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	if strings.TrimSpace(source) == "" {
		return nil, "empty mermaid diagram", false
	}
	if len(source) > mermaidMaxSource {
		return nil, "mermaid diagram exceeds the supported size", false
	}
	diagram, warning := parseMermaid(source)
	if warning != "" {
		return nil, warning, false
	}
	if diagram.sequence {
		return renderSequenceDiagram(diagram, width, theme)
	}
	return renderFlowchart(diagram, width, theme)
}

func parseMermaid(source string) (*mermaidDiagram, string) {
	lines := strings.Split(source, "\n")
	headerIndex := -1
	var header string
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		headerIndex = index
		header = trimmed
		break
	}
	if headerIndex < 0 {
		return nil, "empty mermaid diagram"
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "%%{") {
			return nil, "unsupported mermaid construct: " + trimmed
		}
		if strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if mermaidRejectedLine(trimmed) {
			return nil, "unsupported mermaid construct: " + trimmed
		}
	}
	if strings.HasPrefix(strings.ToLower(header), "sequencediagram") {
		return parseSequenceDiagram(lines[headerIndex+1:])
	}
	lower := strings.ToLower(header)
	if !strings.HasPrefix(lower, "graph") && !strings.HasPrefix(lower, "flowchart") {
		return nil, "unsupported mermaid diagram type"
	}
	fields := strings.Fields(header)
	if len(fields) != 2 {
		return nil, "mermaid flowchart needs an explicit direction"
	}
	direction := strings.ToUpper(fields[1])
	switch direction {
	case "TD", "TB", "LR", "RL", "BT":
	default:
		return nil, "unsupported mermaid direction " + direction
	}
	diagram := &mermaidDiagram{direction: direction}
	seen := map[string]int{}
	for _, line := range lines[headerIndex+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if warning := parseFlowchartLine(diagram, seen, trimmed); warning != "" {
			return nil, warning
		}
	}
	if len(diagram.nodes) == 0 {
		return nil, "mermaid flowchart has no nodes"
	}
	if len(diagram.nodes) > mermaidMaxNodes || len(diagram.edges) > mermaidMaxEdges {
		return nil, "mermaid flowchart exceeds the supported budget"
	}
	if warning := validateAcyclic(diagram); warning != "" {
		return nil, warning
	}
	return diagram, ""
}

func mermaidRejectedLine(line string) bool {
	if strings.Contains(line, "%%{") {
		return true
	}
	lower := strings.ToLower(line)
	for _, prefix := range []string{"subgraph", "style ", "classdef", "class ", "click ", "linkstyle", "direction ", "note ", "loop ", "alt ", "else ", "opt ", "par ", "critical ", "break ", "rect ", "end "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(lower, "end") && (len(lower) == 3 || lower[3] == ' ') {
		return true
	}
	if looksLikeHTMLTag(line) {
		return true
	}
	return false
}

func looksLikeHTMLTag(line string) bool {
	for index := 0; index+1 < len(line); index++ {
		if line[index] != '<' {
			continue
		}
		next := line[index+1]
		if next == '/' || next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' {
			return true
		}
	}
	return false
}

func parseFlowchartLine(diagram *mermaidDiagram, seen map[string]int, line string) string {
	segments, arrows, warning := splitFlowLine(line)
	if warning != "" {
		return warning
	}
	if len(segments) == 0 {
		return ""
	}
	var previous string
	for index, segment := range segments {
		node, warning := parseNodeReference(segment)
		if warning != "" {
			return warning
		}
		if existingIndex, ok := seen[node.id]; ok {
			existing := diagram.nodes[existingIndex]
			if node.shape == ' ' {
				if index == 0 && len(segments) == 1 {
					if strings.Contains(segment, "--") || strings.Contains(segment, "==") {
						return "unsupported mermaid edge syntax"
					}
				}
			} else if existing.shape == ' ' {
				node.order = existing.order
				diagram.nodes[existingIndex] = node
			} else if existing.label != node.label || existing.shape != node.shape {
				return "conflicting definition for node " + node.id
			}
		} else {
			if node.shape == ' ' && len(segments) == 1 {
				if strings.Contains(segment, "--") || strings.Contains(segment, "==") {
					return "unsupported mermaid edge syntax"
				}
			}
			node.order = len(diagram.nodes)
			seen[node.id] = len(diagram.nodes)
			diagram.nodes = append(diagram.nodes, node)
		}
		if index > 0 {
			edge := mermaidEdge{from: previous, to: node.id}
			if index-1 < len(arrows) {
				edge.label = arrows[index-1]
			} else {
				return "unsupported mermaid edge syntax"
			}
			diagram.edges = append(diagram.edges, edge)
		}
		previous = node.id
	}
	if len(arrows) != len(segments)-1 {
		return "unsupported mermaid edge syntax"
	}
	return ""
}

func splitFlowLine(line string) ([]string, []string, string) {
	var segments []string
	var arrows []string
	cursor := 0
	for {
		index, end, label, ok := findArrow(line, cursor)
		if !ok {
			break
		}
		segment := strings.TrimSpace(line[cursor:index])
		if segment == "" {
			return nil, nil, "unsupported mermaid edge syntax"
		}
		if len(strings.TrimSpace(label)) > mermaidMaxLabel {
			return nil, nil, "mermaid edge label is too long"
		}
		segments = append(segments, segment)
		arrows = append(arrows, strings.TrimSpace(label))
		cursor = end
	}
	tail := strings.TrimSpace(line[cursor:])
	if tail == "" {
		if len(segments) == 0 {
			return nil, nil, "unsupported mermaid statement"
		}
		return nil, nil, "unsupported mermaid edge syntax"
	}
	if len(segments) == 0 {
		return []string{tail}, nil, ""
	}
	segments = append(segments, tail)
	return segments, arrows, ""
}

func findArrow(line string, start int) (int, int, string, bool) {
	depthSquare := 0
	depthParen := 0
	depthBrace := 0
	inSingle := false
	inDouble := false
	for index := start; index < len(line); index++ {
		c := line[index]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c == '[' {
			depthSquare++
			continue
		}
		if c == ']' {
			if depthSquare > 0 {
				depthSquare--
			}
			continue
		}
		if c == '(' {
			depthParen++
			continue
		}
		if c == ')' {
			if depthParen > 0 {
				depthParen--
			}
			continue
		}
		if c == '{' {
			depthBrace++
			continue
		}
		if c == '}' {
			if depthBrace > 0 {
				depthBrace--
			}
			continue
		}
		if depthSquare > 0 || depthParen > 0 || depthBrace > 0 {
			continue
		}
		rest := line[index:]
		if strings.HasPrefix(rest, "==>") {
			label, end := trailingPipeLabel(line, index+3)
			return index, end, label, true
		}
		if strings.HasPrefix(rest, "-->") {
			label, end := trailingPipeLabel(line, index+3)
			return index, end, label, true
		}
		if strings.HasPrefix(rest, "--") {
			if end, label, ok := labelledArrow(line, index); ok {
				return index, end, label, true
			}
		}
	}
	return 0, 0, "", false
}

func trailingPipeLabel(line string, index int) (string, int) {
	rest := line[index:]
	if !strings.HasPrefix(rest, "|") {
		return "", index
	}
	close := strings.IndexByte(rest[1:], '|')
	if close < 0 {
		return "", index
	}
	return rest[1 : 1+close], index + 1 + close + 1
}

func labelledArrow(line string, index int) (int, string, bool) {
	rest := line[index+2:]
	close := strings.Index(rest, "--")
	if close < 0 {
		return 0, "", false
	}
	end := index + 2 + close + 2
	if end >= len(line) || line[end] != '>' {
		return 0, "", false
	}
	label := strings.TrimSpace(rest[:close])
	if label == "" {
		return 0, "", false
	}
	if strings.Contains(label, "|") || strings.Contains(label, "[") || strings.Contains(label, "]") || strings.Contains(label, "{") || strings.Contains(label, "}") {
		return 0, "", false
	}
	return end + 1, label, true
}

func parseNodeReference(segment string) (mermaidNode, string) {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return mermaidNode{}, "unsupported mermaid edge syntax"
	}
	idEnd := 0
	for idEnd < len(segment) && (isIdentPart(segment[idEnd]) || segment[idEnd] == '-') {
		idEnd++
	}
	if idEnd == 0 {
		return mermaidNode{}, "unsupported mermaid node identifier"
	}
	id := segment[:idEnd]
	if strings.Contains(id, "--") || strings.Contains(id, "==") {
		return mermaidNode{}, "unsupported mermaid edge syntax"
	}
	node := mermaidNode{id: id, label: id, shape: ' '}
	rest := strings.TrimSpace(segment[idEnd:])
	if rest == "" {
		return node, ""
	}
	open := rest[0]
	var close byte
	switch open {
	case '[':
		close = ']'
	case '(':
		close = ')'
	case '{':
		close = '}'
	default:
		return mermaidNode{}, "unsupported mermaid node shape"
	}
	if rest[len(rest)-1] != close {
		return mermaidNode{}, "unsupported mermaid node shape"
	}
	label := strings.TrimSpace(rest[1 : len(rest)-1])
	label = strings.Trim(trimQuotes(label), " ")
	if label == "" {
		label = node.id
	}
	if len([]rune(label)) > mermaidMaxLabel {
		return mermaidNode{}, "mermaid node label is too long"
	}
	node.label = SanitizeSingleLine(label)
	node.shape = open
	return node, ""
}

func trimQuotes(text string) string {
	if len(text) >= 2 && (text[0] == '"' && text[len(text)-1] == '"' || text[0] == '\'' && text[len(text)-1] == '\'') {
		return text[1 : len(text)-1]
	}
	return text
}

func validateAcyclic(diagram *mermaidDiagram) string {
	adjacency := map[string][]string{}
	for _, edge := range diagram.edges {
		adjacency[edge.from] = append(adjacency[edge.from], edge.to)
	}
	colors := map[string]int{}
	var visit func(node string) bool
	visit = func(node string) bool {
		switch colors[node] {
		case 1:
			return true
		case 2:
			return false
		}
		colors[node] = 1
		for _, next := range adjacency[node] {
			if visit(next) {
				return true
			}
		}
		colors[node] = 2
		return false
	}
	for _, node := range diagram.nodes {
		if visit(node.id) {
			return "mermaid cycles are not supported"
		}
	}
	return ""
}

func isValidSequenceName(name string) bool {
	if name == "" {
		return false
	}
	if len([]rune(name)) > mermaidMaxLabel {
		return false
	}
	for index := 0; index < len(name); index++ {
		c := name[index]
		if isIdentPart(c) || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func parseSequenceDiagram(lines []string) (*mermaidDiagram, string) {
	diagram := &mermaidDiagram{sequence: true, aliases: map[string]string{}}
	known := map[string]bool{}
	register := func(name string) string {
		name = strings.TrimSpace(name)
		if name == "" {
			return ""
		}
		if !isValidSequenceName(name) {
			return ""
		}
		if len(known) >= mermaidMaxParticipants && !known[name] {
			return ""
		}
		if !known[name] {
			known[name] = true
			diagram.participants = append(diagram.participants, name)
		}
		return name
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "participant ") || strings.HasPrefix(lower, "actor ") {
			space := strings.IndexByte(trimmed, ' ')
			if space < 0 {
				return nil, "unsupported mermaid sequence statement"
			}
			declaration := strings.TrimSpace(trimmed[space+1:])
			if declaration == "" {
				return nil, "unsupported mermaid sequence statement"
			}
			name := declaration
			label := declaration
			if asIndex := strings.Index(strings.ToLower(declaration), " as "); asIndex >= 0 {
				name = strings.TrimSpace(declaration[:asIndex])
				label = strings.TrimSpace(declaration[asIndex+4:])
			}
			if !isValidSequenceName(name) {
				return nil, "unsupported mermaid sequence statement"
			}
			if label == "" {
				label = name
			}
			if len([]rune(label)) > mermaidMaxLabel {
				return nil, "mermaid sequence exceeds the participant budget"
			}
			if register(name) == "" {
				return nil, "mermaid sequence exceeds the participant budget"
			}
			diagram.aliases[name] = SanitizeSingleLine(label)
			continue
		}
		message, ok := parseSequenceMessage(trimmed)
		if !ok {
			return nil, "unsupported mermaid sequence statement"
		}
		if register(message.from) == "" || register(message.to) == "" {
			return nil, "mermaid sequence exceeds the participant budget"
		}
		if len(diagram.messages) >= mermaidMaxMessages {
			return nil, "mermaid sequence exceeds the message budget"
		}
		message.label = SanitizeSingleLine(message.label)
		diagram.messages = append(diagram.messages, message)
	}
	if len(diagram.participants) == 0 {
		return nil, "mermaid sequence has no participants"
	}
	return diagram, ""
}

func parseSequenceMessage(line string) (mermaidMessage, bool) {
	arrowIndex := -1
	arrow := ""
	dashed := false
	for index := 0; index < len(line); index++ {
		rest := line[index:]
		switch {
		case strings.HasPrefix(rest, "-->>"):
			arrowIndex = index
			arrow = "-->>"
			dashed = true
		case strings.HasPrefix(rest, "->>"):
			arrowIndex = index
			arrow = "->>"
			dashed = false
		case strings.HasPrefix(rest, "-->"):
			arrowIndex = index
			arrow = "-->"
			dashed = true
		case strings.HasPrefix(rest, "->"):
			arrowIndex = index
			arrow = "->"
			dashed = false
		}
		if arrowIndex >= 0 {
			break
		}
	}
	if arrowIndex < 0 {
		return mermaidMessage{}, false
	}
	from := strings.TrimSpace(line[:arrowIndex])
	remainder := line[arrowIndex:]
	if !strings.HasPrefix(remainder, arrow) {
		return mermaidMessage{}, false
	}
	colon := strings.IndexByte(remainder, ':')
	if colon < 0 {
		return mermaidMessage{}, false
	}
	to := strings.TrimSpace(remainder[len(sequenceArrow(remainder)):colon])
	label := strings.TrimSpace(remainder[colon+1:])
	if !isValidSequenceName(from) || !isValidSequenceName(to) {
		return mermaidMessage{}, false
	}
	if len([]rune(label)) > mermaidMaxLabel {
		return mermaidMessage{}, false
	}
	return mermaidMessage{from: from, to: to, label: label, dashed: dashed}, true
}

func sequenceArrow(remainder string) string {
	for _, arrow := range []string{"-->>", "->>", "-->", "->"} {
		if strings.HasPrefix(remainder, arrow) {
			return arrow
		}
	}
	return "->"
}

func renderSequenceDiagram(diagram *mermaidDiagram, width int, theme *tui.Theme) ([]string, string, bool) {
	participantNames := make([]string, 0, len(diagram.participants))
	for _, participant := range diagram.participants {
		participantNames = append(participantNames, diagram.participantLabel(participant))
	}
	lines := []string{theme.Fg("muted", "participants: "+strings.Join(participantNames, ", "))}
	arrow := "──▶"
	for _, message := range diagram.messages {
		if message.dashed {
			arrow = "╌╌▶"
		} else {
			arrow = "──▶"
		}
		from := diagram.participantLabel(message.from)
		to := diagram.participantLabel(message.to)
		line := theme.Fg("accent", SanitizeSingleLine(from)) + " " +
			theme.Fg("border", arrow) + " " +
			theme.Fg("accent", SanitizeSingleLine(to))
		if message.label != "" {
			line += theme.Fg("text", ": "+message.label)
		}
		lines = append(lines, line)
	}
	for _, line := range lines {
		if tui.VisibleWidth(line) > width {
			return nil, "mermaid sequence diagram does not fit the available width", false
		}
	}
	return lines, "", true
}

func renderFlowchart(diagram *mermaidDiagram, width int, theme *tui.Theme) ([]string, string, bool) {
	if diagram.direction == "LR" || diagram.direction == "RL" {
		return renderFlowchartHorizontal(diagram, width, theme)
	}
	return renderFlowchartVertical(diagram, width, theme)
}

func renderFlowchartVertical(diagram *mermaidDiagram, width int, theme *tui.Theme) ([]string, string, bool) {
	ranks, order := mermaidRanks(diagram)
	boxWidth := 5
	for _, node := range diagram.nodes {
		if candidate := tui.VisibleWidth(node.label) + 4; candidate > boxWidth {
			boxWidth = candidate
		}
	}
	direction := diagram.direction
	arrow := "▼"
	if direction == "BT" {
		arrow = "▲"
	}
	var lines []string
	for _, rank := range order {
		for _, nodeID := range ranks[rank] {
			node := mermaidNodeByID(diagram, nodeID)
			if node == nil {
				continue
			}
			indent := strings.Repeat("  ", rank)
			lines = append(lines, renderMermaidBox(*node, boxWidth, theme, indent)...)
			outgoing := mermaidOutgoing(diagram, nodeID)
			for index, edge := range outgoing {
				connector := "├─"
				if index == len(outgoing)-1 {
					connector = "└─"
				}
				target := mermaidNodeByID(diagram, edge.to)
				targetLabel := edge.to
				if target != nil {
					targetLabel = target.label
				}
				label := ""
				if edge.label != "" {
					label = " " + SanitizeSingleLine(edge.label)
				}
				line := indent + "  " + theme.Fg("border", connector) + theme.Fg("accent", label+" "+arrow+" ") + theme.Fg("text", "["+targetLabel+"]")
				lines = append(lines, line)
			}
		}
		lines = append(lines, "")
	}
	for len(lines) > 0 && strings.TrimSpace(tui.StripTerminalSequences(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		if tui.VisibleWidth(line) > width {
			return nil, "mermaid flowchart does not fit the available width", false
		}
	}
	return lines, "", true
}

func renderFlowchartHorizontal(diagram *mermaidDiagram, width int, theme *tui.Theme) ([]string, string, bool) {
	ranks, _ := mermaidRanks(diagram)
	rankOf := map[string]int{}
	for rank, ids := range ranks {
		for _, id := range ids {
			rankOf[id] = rank
		}
	}
	ordered := make([]mermaidNode, len(diagram.nodes))
	copy(ordered, diagram.nodes)
	isRL := diagram.direction == "RL"
	sort.Slice(ordered, func(i, j int) bool {
		ri := rankOf[ordered[i].id]
		rj := rankOf[ordered[j].id]
		if ri != rj {
			if isRL {
				return ri > rj
			}
			return ri < rj
		}
		if isRL {
			return ordered[i].order > ordered[j].order
		}
		return ordered[i].order < ordered[j].order
	})
	boxWidth := 5
	for _, node := range diagram.nodes {
		if candidate := tui.VisibleWidth(node.label) + 4; candidate > boxWidth {
			boxWidth = candidate
		}
	}
	tops := make([]string, 0, len(ordered))
	mids := make([]string, 0, len(ordered))
	bots := make([]string, 0, len(ordered))
	for _, node := range ordered {
		box := renderMermaidBox(node, boxWidth, theme, "")
		if len(box) != 3 {
			return nil, "mermaid flowchart does not fit the available width", false
		}
		tops = append(tops, box[0])
		mids = append(mids, box[1])
		bots = append(bots, box[2])
	}
	lines := []string{
		strings.Join(tops, "  "),
		strings.Join(mids, "  "),
		strings.Join(bots, "  "),
		"",
	}
	arrow := "──▶"
	if isRL {
		arrow = "◀──"
	}
	for _, edge := range diagram.edges {
		from := mermaidNodeByID(diagram, edge.from)
		to := mermaidNodeByID(diagram, edge.to)
		fromLabel := edge.from
		if from != nil {
			fromLabel = from.label
		}
		toLabel := edge.to
		if to != nil {
			toLabel = to.label
		}
		labelPart := ""
		if edge.label != "" {
			labelPart = SanitizeSingleLine(edge.label) + " "
		}
		line := theme.Fg("accent", SanitizeSingleLine(fromLabel)) + " " + theme.Fg("border", arrow) + " " + theme.Fg("text", labelPart+"["+SanitizeSingleLine(toLabel)+"]")
		lines = append(lines, line)
	}
	for len(lines) > 0 && strings.TrimSpace(tui.StripTerminalSequences(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		if tui.VisibleWidth(line) > width {
			return nil, "mermaid flowchart does not fit the available width", false
		}
	}
	return lines, "", true
}

func mermaidNodeByID(diagram *mermaidDiagram, id string) *mermaidNode {
	for index := range diagram.nodes {
		if diagram.nodes[index].id == id {
			return &diagram.nodes[index]
		}
	}
	return nil
}

func mermaidOutgoing(diagram *mermaidDiagram, id string) []mermaidEdge {
	var result []mermaidEdge
	for _, edge := range diagram.edges {
		if edge.from == id {
			result = append(result, edge)
		}
	}
	return result
}

func mermaidRanks(diagram *mermaidDiagram) (map[int][]string, []int) {
	rank := map[string]int{}
	for _, node := range diagram.nodes {
		rank[node.id] = 0
	}
	for iteration := 0; iteration <= len(diagram.nodes); iteration++ {
		changed := false
		for _, edge := range diagram.edges {
			if rank[edge.to] < rank[edge.from]+1 {
				rank[edge.to] = rank[edge.from] + 1
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	maxRank := 0
	for _, value := range rank {
		if value > maxRank {
			maxRank = value
		}
	}
	ranks := map[int][]string{}
	for _, node := range diagram.nodes {
		ranks[rank[node.id]] = append(ranks[rank[node.id]], node.id)
	}
	order := make([]int, 0, maxRank+1)
	if diagram.direction == "BT" {
		for value := maxRank; value >= 0; value-- {
			order = append(order, value)
		}
	} else {
		for value := 0; value <= maxRank; value++ {
			order = append(order, value)
		}
	}
	return ranks, order
}

func (d *mermaidDiagram) participantLabel(id string) string {
	if label, ok := d.aliases[id]; ok && label != "" {
		return label
	}
	return id
}

func renderMermaidBox(node mermaidNode, boxWidth int, theme *tui.Theme, indent string) []string {
	inner := boxWidth - 2
	label := node.label
	labelWidth := tui.VisibleWidth(label)
	if labelWidth > inner {
		label = tui.TruncateToWidth(label, inner, "…", false)
		labelWidth = tui.VisibleWidth(label)
	}
	leftPad := (inner - labelWidth) / 2
	rightPad := inner - labelWidth - leftPad
	var top, bottom string
	switch node.shape {
	case '(':
		top = "╭" + strings.Repeat("─", inner) + "╮"
		bottom = "╰" + strings.Repeat("─", inner) + "╯"
	case '{':
		top = "╱" + strings.Repeat("─", inner) + "╲"
		bottom = "╲" + strings.Repeat("─", inner) + "╱"
	default:
		top = "┌" + strings.Repeat("─", inner) + "┐"
		bottom = "└" + strings.Repeat("─", inner) + "┘"
	}
	return []string{
		indent + theme.Fg("border", top),
		indent + theme.Fg("border", "│") + strings.Repeat(" ", leftPad) + theme.Fg("text", label) + strings.Repeat(" ", rightPad) + theme.Fg("border", "│"),
		indent + theme.Fg("border", bottom),
	}
}
