package interactive

import (
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	TreeFilterDefault  = "default"
	TreeFilterNoTools  = "no-tools"
	TreeFilterUser     = "user"
	TreeFilterLabelled = "labelled"
	TreeFilterAll      = "all"
)

var treeFilterOrder = []string{TreeFilterDefault, TreeFilterNoTools, TreeFilterUser, TreeFilterLabelled, TreeFilterAll}

const (
	TreeActionSelect = "select"
	TreeActionCopy   = "copy"
	TreeActionDelete = "delete"
	TreeActionRename = "rename"
	TreeActionLabel  = "label"
	TreeActionClose  = "close"
)

const treeMaxDisplayDepth = 8

type TreeBrowserNode struct {
	ID             string
	ParentID       string
	Depth          int
	Kind           string
	CustomType     string
	Display        bool
	Label          string
	LabelTimestamp string
	Timestamp      string
	Preview        string
	Active         bool
	Leaf           bool
	Corrupt        bool
	Deletable      bool
	Renamable      bool
}

type TreeBrowserOptions struct {
	Title          string
	Nodes          []TreeBrowserNode
	ActiveEntryID  string
	Filter         string
	HideTimestamps bool
}

type TreeBrowserAction struct {
	Kind           string
	EntryID        string
	Node           TreeBrowserNode
	Filter         string
	HideTimestamps bool
}

type TreeBrowser struct {
	mu             sync.Mutex
	theme          DialogTheme
	title          string
	nodes          []TreeBrowserNode
	folded         map[string]bool
	filter         string
	search         string
	searching      bool
	selected       string
	focused        bool
	settled        bool
	scroll         int
	hideTimestamps bool
	onAction       func(TreeBrowserAction)
}

func NewTreeBrowser(opts TreeBrowserOptions, theme DialogTheme, onAction func(TreeBrowserAction)) *TreeBrowser {
	filter := opts.Filter
	if !validTreeFilter(filter) {
		filter = TreeFilterDefault
	}
	browser := &TreeBrowser{
		theme:          theme,
		title:          SanitizeSingleLine(opts.Title),
		nodes:          cloneTreeNodes(opts.Nodes),
		folded:         map[string]bool{},
		filter:         filter,
		selected:       opts.ActiveEntryID,
		hideTimestamps: opts.HideTimestamps,
		onAction:       onAction,
	}
	if browser.selected == "" {
		for _, node := range browser.nodes {
			if node.Leaf {
				browser.selected = node.ID
				break
			}
		}
	}
	if browser.selected == "" && len(browser.nodes) > 0 {
		browser.selected = browser.nodes[0].ID
	}
	browser.reconcileSelectionLocked()
	return browser
}

func cloneTreeNodes(nodes []TreeBrowserNode) []TreeBrowserNode {
	out := make([]TreeBrowserNode, len(nodes))
	copy(out, nodes)
	return out
}

func validTreeFilter(filter string) bool {
	for _, candidate := range treeFilterOrder {
		if candidate == filter {
			return true
		}
	}
	return false
}

func (b *TreeBrowser) SetFocused(focused bool) {
	b.mu.Lock()
	b.focused = focused
	b.mu.Unlock()
}

func (b *TreeBrowser) SetTheme(theme DialogTheme) {
	b.mu.Lock()
	b.theme = theme
	b.mu.Unlock()
}

func (b *TreeBrowser) Invalidate() {}

func (b *TreeBrowser) Filter() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.filter
}

func (b *TreeBrowser) SelectedID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.selected
}

func (b *TreeBrowser) SelectedPreview() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.selectedNodeLocked().Preview
}

func (b *TreeBrowser) Search() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.search
}

func (b *TreeBrowser) Folded() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.folded)
}

func (b *TreeBrowser) TimestampsVisible() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.hideTimestamps
}

func (b *TreeBrowser) HideTimestamps() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hideTimestamps
}

func (b *TreeBrowser) DisplayDepth(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	visible, depths := b.computeVisibleLocked()
	for _, index := range visible {
		if b.nodes[index].ID == id {
			return depths[index]
		}
	}
	return -1
}

func (b *TreeBrowser) VisibleIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	visible := b.visibleIndicesLocked()
	out := make([]string, 0, len(visible))
	for _, index := range visible {
		out = append(out, b.nodes[index].ID)
	}
	return out
}

func (b *TreeBrowser) selectedNodeLocked() TreeBrowserNode {
	for _, node := range b.nodes {
		if node.ID == b.selected {
			return node
		}
	}
	return TreeBrowserNode{}
}

func (b *TreeBrowser) nodeIndexLocked(id string) int {
	for index, node := range b.nodes {
		if node.ID == id {
			return index
		}
	}
	return -1
}

func (b *TreeBrowser) nodeVisibleLocked(id string) bool {
	if id == "" {
		return false
	}
	for _, index := range b.visibleIndicesLocked() {
		if b.nodes[index].ID == id {
			return true
		}
	}
	return false
}

func (b *TreeBrowser) reconcileSelectionLocked() {
	visible := b.visibleIndicesLocked()
	if len(visible) == 0 {
		b.selected = ""
		b.scroll = 0
		return
	}
	selectedIndex := b.nodeIndexLocked(b.selected)
	position := -1
	for offset, index := range visible {
		if index <= selectedIndex {
			position = offset
			continue
		}
		break
	}
	if position < 0 {
		position = 0
	}
	b.selected = b.nodes[visible[position]].ID
	if position < b.scroll {
		b.scroll = position
	} else if position >= b.scroll+treeBrowserWindow {
		b.scroll = position - treeBrowserWindow + 1
		if b.scroll < 0 {
			b.scroll = 0
		}
	}
}

func isTreeBookkeepingKind(kind string) bool {
	switch kind {
	case "label", "info", "model", "thinking", "notice", "custom", "provenance", "profile", "custom-hidden":
		return true
	default:
		return false
	}
}

func isTreeConversationKind(kind string) bool {
	return !isTreeBookkeepingKind(kind)
}

func treeFilterMatches(filter string, node TreeBrowserNode) bool {
	switch filter {
	case TreeFilterNoTools:
		return !isTreeBookkeepingKind(node.Kind) && node.Kind != "tool"
	case TreeFilterUser:
		return node.Kind == "user"
	case TreeFilterLabelled:
		return strings.TrimSpace(node.Label) != ""
	case TreeFilterAll:
		return true
	default:
		return !isTreeBookkeepingKind(node.Kind)
	}
}

func treeSearchMatches(query string, node TreeBrowserNode) bool {
	if query == "" {
		return true
	}
	lower := strings.ToLower(query)
	return strings.Contains(strings.ToLower(node.Kind), lower) ||
		strings.Contains(strings.ToLower(node.Label), lower) ||
		strings.Contains(strings.ToLower(node.Preview), lower) ||
		strings.Contains(strings.ToLower(node.ID), lower)
}

func (b *TreeBrowser) visibleIndicesLocked() []int {
	visible, _ := b.computeVisibleLocked()
	return visible
}

func (b *TreeBrowser) computeVisibleLocked() ([]int, map[int]int) {
	n := len(b.nodes)
	depths := make(map[int]int, n)
	if n == 0 {
		return nil, depths
	}
	indexByID := make(map[string]int, n)
	for i, node := range b.nodes {
		if _, ok := indexByID[node.ID]; !ok && node.ID != "" {
			indexByID[node.ID] = i
		}
	}
	parentIdx := make([]int, n)
	for i := range parentIdx {
		parentIdx[i] = -1
	}
	for i, node := range b.nodes {
		if node.ParentID == "" {
			continue
		}
		if p, ok := indexByID[node.ParentID]; ok && p != i {
			parentIdx[i] = p
		}
	}
	matches := make([]bool, n)
	for i, node := range b.nodes {
		if !treeFilterMatches(b.filter, node) || !treeSearchMatches(b.search, node) {
			continue
		}
		matches[i] = true
	}
	visible := make([]bool, n)
	for i, m := range matches {
		if m {
			visible[i] = true
		}
	}
	if strings.TrimSpace(b.search) != "" {
		for j := range b.nodes {
			if !matches[j] {
				continue
			}
			seen := map[int]bool{j: true}
			cur := parentIdx[j]
			steps := 0
			for cur >= 0 && steps <= n {
				if seen[cur] {
					break
				}
				seen[cur] = true
				if treeFilterMatches(b.filter, b.nodes[cur]) {
					visible[cur] = true
				}
				cur = parentIdx[cur]
				steps++
			}
		}
	}
	displayParent := make(map[int]int, n)
	for i := range b.nodes {
		if !visible[i] {
			continue
		}
		seen := map[int]bool{}
		cur := parentIdx[i]
		found := -1
		steps := 0
		for cur >= 0 && steps <= n {
			if seen[cur] {
				break
			}
			seen[cur] = true
			if visible[cur] {
				found = cur
				break
			}
			cur = parentIdx[cur]
			steps++
		}
		displayParent[i] = found
		if found < 0 {
			depths[i] = 0
		} else {
			d := depths[found] + 1
			if d > treeMaxDisplayDepth {
				d = treeMaxDisplayDepth
			}
			depths[i] = d
		}
	}
	out := make([]int, 0, n)
	for i := range b.nodes {
		if !visible[i] {
			continue
		}
		cur := displayParent[i]
		hidden := false
		seen := map[int]bool{}
		steps := 0
		for cur >= 0 && steps <= n {
			if seen[cur] {
				break
			}
			seen[cur] = true
			if b.folded[b.nodes[cur].ID] {
				hidden = true
				break
			}
			cur = displayParent[cur]
			steps++
		}
		if !hidden {
			out = append(out, i)
		}
	}
	return out, depths
}

func (b *TreeBrowser) filterVisibleLocked() ([]int, map[int]bool) {
	n := len(b.nodes)
	if n == 0 {
		return nil, map[int]bool{}
	}
	indexByID := make(map[string]int, n)
	for i, node := range b.nodes {
		if _, ok := indexByID[node.ID]; !ok && node.ID != "" {
			indexByID[node.ID] = i
		}
	}
	parentIdx := make([]int, n)
	for i := range parentIdx {
		parentIdx[i] = -1
	}
	for i, node := range b.nodes {
		if node.ParentID == "" {
			continue
		}
		if p, ok := indexByID[node.ParentID]; ok && p != i {
			parentIdx[i] = p
		}
	}
	matches := make([]bool, n)
	for i, node := range b.nodes {
		if !treeFilterMatches(b.filter, node) || !treeSearchMatches(b.search, node) {
			continue
		}
		matches[i] = true
	}
	visible := make([]bool, n)
	for i, m := range matches {
		if m {
			visible[i] = true
		}
	}
	if len(b.search) != 0 && len(matches) > 0 {
		trimmed := false
		for _, r := range b.search {
			if r != ' ' && r != '\t' && r != '\n' {
				trimmed = true
				break
			}
		}
		if trimmed {
			for j := range b.nodes {
				if !matches[j] {
					continue
				}
				seen := map[int]bool{j: true}
				cur := parentIdx[j]
				steps := 0
				for cur >= 0 && steps <= n {
					if seen[cur] {
						break
					}
					seen[cur] = true
					if treeFilterMatches(b.filter, b.nodes[cur]) {
						visible[cur] = true
					}
					cur = parentIdx[cur]
					steps++
				}
			}
		}
	}
	out := make([]int, 0, n)
	set := make(map[int]bool, n)
	for i := range b.nodes {
		if visible[i] {
			out = append(out, i)
			set[i] = true
		}
	}
	return out, set
}

func (b *TreeBrowser) hasVisibleChildrenLocked(id string) bool {
	visible, visibleSet := b.filterVisibleLocked()
	indexByID := make(map[string]int, len(b.nodes))
	for i, node := range b.nodes {
		if _, ok := indexByID[node.ID]; !ok && node.ID != "" {
			indexByID[node.ID] = i
		}
	}
	for _, child := range visible {
		curParent := b.nodes[child].ParentID
		steps := 0
		for curParent != "" && steps <= len(b.nodes) {
			pIdx, ok := indexByID[curParent]
			if !ok {
				break
			}
			if !visibleSet[pIdx] {
				curParent = b.nodes[pIdx].ParentID
				steps++
				continue
			}
			if b.nodes[pIdx].ID == id {
				return true
			}
			break
		}
	}
	return false
}

func (b *TreeBrowser) hasChildrenLocked(id string) bool {
	return b.hasVisibleChildrenLocked(id)
}

func (b *TreeBrowser) moveLocked(delta int) {
	visible := b.visibleIndicesLocked()
	if len(visible) == 0 {
		return
	}
	position := -1
	for offset, index := range visible {
		if b.nodes[index].ID == b.selected {
			position = offset
			break
		}
	}
	if position < 0 {
		position = 0
	} else {
		position += delta
	}
	if position < 0 {
		position = 0
	}
	if position >= len(visible) {
		position = len(visible) - 1
	}
	b.selected = b.nodes[visible[position]].ID
	if position < b.scroll {
		b.scroll = position
	} else if position >= b.scroll+treeBrowserWindow {
		b.scroll = position - treeBrowserWindow + 1
		if b.scroll < 0 {
			b.scroll = 0
		}
	}
}

func (b *TreeBrowser) toggleFoldLocked() {
	node := b.selectedNodeLocked()
	if node.ID == "" || !b.hasChildrenLocked(node.ID) {
		return
	}
	b.folded[node.ID] = !b.folded[node.ID]
}

func (b *TreeBrowser) foldLocked(fold bool) {
	node := b.selectedNodeLocked()
	if node.ID == "" || !b.hasChildrenLocked(node.ID) {
		return
	}
	if !fold {
		delete(b.folded, node.ID)
		return
	}
	b.folded[node.ID] = true
}

func (b *TreeBrowser) toggleTimestampsLocked() {
	b.hideTimestamps = !b.hideTimestamps
}

func (b *TreeBrowser) cycleFilterLocked() string {
	for index, filter := range treeFilterOrder {
		if filter == b.filter {
			b.filter = treeFilterOrder[(index+1)%len(treeFilterOrder)]
			b.reconcileSelectionLocked()
			return b.filter
		}
	}
	b.filter = TreeFilterDefault
	b.reconcileSelectionLocked()
	return b.filter
}

func (b *TreeBrowser) actionableLocked(kind string, eligible func(TreeBrowserNode) bool) (TreeBrowserAction, func(TreeBrowserAction), bool) {
	if b.settled {
		return TreeBrowserAction{}, nil, false
	}
	node := b.selectedNodeLocked()
	if kind != TreeActionClose {
		if node.ID == "" || !b.nodeVisibleLocked(node.ID) {
			return TreeBrowserAction{}, nil, false
		}
		if eligible != nil && !eligible(node) {
			return TreeBrowserAction{}, nil, false
		}
	}
	switch kind {
	case TreeActionClose, TreeActionSelect, TreeActionDelete, TreeActionRename, TreeActionLabel:
		b.settled = true
	}
	return TreeBrowserAction{Kind: kind, EntryID: node.ID, Node: node, Filter: b.filter, HideTimestamps: b.hideTimestamps}, b.onAction, true
}

func (b *TreeBrowser) emit(kind string) {
	b.emitIf(kind, nil)
}

func (b *TreeBrowser) emitIf(kind string, eligible func(TreeBrowserNode) bool) {
	b.mu.Lock()
	action, callback, ok := b.actionableLocked(kind, eligible)
	b.mu.Unlock()
	if !ok || callback == nil {
		return
	}
	callback(action)
}

const treeBrowserWindow = 12

func (b *TreeBrowser) Render(width int) []string {
	b.mu.Lock()
	nodes := b.nodes
	visible, depths := b.computeVisibleLocked()
	selected := b.selected
	scroll := b.scroll
	theme := b.theme
	title := b.title
	filter := b.filter
	hideTimestamps := b.hideTimestamps
	b.mu.Unlock()
	visibleSet := make(map[int]bool, len(visible))
	for _, index := range visible {
		visibleSet[index] = true
	}
	indexByID := make(map[string]int, len(nodes))
	for i, node := range nodes {
		if _, ok := indexByID[node.ID]; !ok && node.ID != "" {
			indexByID[node.ID] = i
		}
	}
	nearestVisibleParent := func(child int) int {
		seen := map[int]bool{}
		curParent := nodes[child].ParentID
		steps := 0
		for curParent != "" && steps <= len(nodes) {
			pIdx, ok := indexByID[curParent]
			if !ok {
				return -1
			}
			if seen[pIdx] {
				return -1
			}
			seen[pIdx] = true
			if visibleSet[pIdx] {
				return pIdx
			}
			curParent = nodes[pIdx].ParentID
			steps++
		}
		return -1
	}
	hasVisibleChild := func(id string) bool {
		for _, index := range visible {
			if nearestVisibleParent(index) >= 0 && nodes[nearestVisibleParent(index)].ID == id {
				return true
			}
		}
		return false
	}
	bodyWidth := dialogBodyWidth(width)
	rows := make([]string, 0, len(visible))
	for _, index := range visible {
		node := nodes[index]
		displayDepth := depths[index]
		if displayDepth < 0 {
			displayDepth = 0
		}
		if displayDepth > treeMaxDisplayDepth {
			displayDepth = treeMaxDisplayDepth
		}
		marker := "  "
		switch {
		case b.isFolded(node.ID):
			marker = "▸ "
		case hasVisibleChild(node.ID):
			marker = "▾ "
		}
		indent := strings.Repeat("  ", displayDepth)
		cursor := "  "
		if node.ID == selected {
			cursor = theme.Accent("› ")
		}
		kind := SanitizeSingleLine(node.Kind)
		label := SanitizeSingleLine(node.Label)
		labelStamp := SanitizeSingleLine(node.LabelTimestamp)
		timestamp := SanitizeSingleLine(node.Timestamp)
		line := cursor + indent + marker + kind
		if label != "" {
			line += " " + theme.Muted("("+label+")")
			if labelStamp != "" && !hideTimestamps {
				line += " " + theme.Dim("["+labelStamp+"]")
			}
		}
		if timestamp != "" && !hideTimestamps {
			stamp := "  " + theme.Dim(timestamp)
			available := bodyWidth - tui.VisibleWidth(line) - tui.VisibleWidth(stamp)
			if available >= 0 {
				line += strings.Repeat(" ", available) + stamp
			}
		}
		body := tui.TruncateToWidth(line, bodyWidth, "…", false)
		if node.ID == selected {
			body = theme.SelectedBg(tui.TruncateToWidth(body, bodyWidth, "…", true))
		}
		rows = append(rows, body)
	}
	if len(rows) == 0 {
		rows = append(rows, theme.Dim("(no entries match the filter)"))
	}
	if scroll > len(rows) {
		scroll = 0
	}
	end := scroll + treeBrowserWindow
	if end > len(rows) {
		end = len(rows)
	}
	body := append([]string{}, rows[scroll:end]...)
	if selectedNode := nodeByID(nodes, selected); selectedNode.Preview != "" {
		body = append(body, theme.Dim("─ preview ─"))
		for _, line := range tui.WrapTextWithANSI(SanitizeDisplayText(selectedNode.Preview), bodyWidth) {
			body = append(body, theme.Dim(tui.TruncateToWidth(line, bodyWidth, "…", false)))
		}
	}
	hint := "↑/↓ move · ←/→ fold · Tab filter · / search · c copy · t ts l · Esc close"
	if b.searchValue() != "" {
		hint = "search: " + SanitizeSingleLine(b.searchValue()) + " · " + hint
	}
	return renderDialogFrame(title+" ["+filter+"]", hint, body, width, theme)
}

func (b *TreeBrowser) isFolded(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.folded[id]
}

func (b *TreeBrowser) searchValue() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.search
}

func nodeByID(nodes []TreeBrowserNode, id string) TreeBrowserNode {
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	return TreeBrowserNode{}
}

func (b *TreeBrowser) HandleInput(data string) {
	keys := tui.GlobalKeybindings()
	b.mu.Lock()
	searching := b.searching
	b.mu.Unlock()
	if searching {
		switch {
		case tui.MatchesKey(data, "backspace"):
			b.mu.Lock()
			if b.search != "" {
				runes := []rune(b.search)
				b.search = string(runes[:len(runes)-1])
				b.reconcileSelectionLocked()
			}
			b.mu.Unlock()
			return
		case tui.MatchesKey(data, "enter"), keys.Matches(data, "tui.select.cancel"):
			b.mu.Lock()
			b.searching = false
			b.mu.Unlock()
			return
		case keys.Matches(data, "tui.select.up"):
			b.mu.Lock()
			b.moveLocked(-1)
			b.mu.Unlock()
			return
		case keys.Matches(data, "tui.select.down"):
			b.mu.Lock()
			b.moveLocked(1)
			b.mu.Unlock()
			return
		default:
			if isPrintableInput(data) {
				b.mu.Lock()
				b.search += SanitizeSingleLine(data)
				b.reconcileSelectionLocked()
				b.mu.Unlock()
			}
			return
		}
	}
	switch {
	case keys.Matches(data, "tui.select.cancel"):
		b.emit(TreeActionClose)
	case keys.Matches(data, "tui.select.up"):
		b.mu.Lock()
		b.moveLocked(-1)
		b.mu.Unlock()
	case keys.Matches(data, "tui.select.down"):
		b.mu.Lock()
		b.moveLocked(1)
		b.mu.Unlock()
	case keys.Matches(data, "tui.select.pageUp"):
		b.mu.Lock()
		b.moveLocked(-treeBrowserWindow)
		b.mu.Unlock()
	case keys.Matches(data, "tui.select.pageDown"):
		b.mu.Lock()
		b.moveLocked(treeBrowserWindow)
		b.mu.Unlock()
	case tui.MatchesKey(data, "left"):
		b.mu.Lock()
		b.foldLocked(true)
		b.mu.Unlock()
	case tui.MatchesKey(data, "right"):
		b.mu.Lock()
		b.foldLocked(false)
		b.mu.Unlock()
	case tui.MatchesKey(data, "enter"), tui.MatchesKey(data, " "):
		b.mu.Lock()
		node := b.selectedNodeLocked()
		if node.ID != "" && b.hasChildrenLocked(node.ID) {
			b.toggleFoldLocked()
			b.mu.Unlock()
			return
		}
		action, callback, ok := b.actionableLocked(TreeActionSelect, nil)
		b.mu.Unlock()
		if ok && callback != nil {
			callback(action)
		}
	case tui.MatchesKey(data, "tab"):
		b.mu.Lock()
		b.cycleFilterLocked()
		b.mu.Unlock()
	case data == "/":
		b.mu.Lock()
		b.searching = true
		b.mu.Unlock()
	case tui.MatchesKey(data, "t"):
		b.mu.Lock()
		b.toggleTimestampsLocked()
		b.mu.Unlock()
	case tui.MatchesKey(data, "l"):
		b.emit(TreeActionLabel)
	case tui.MatchesKey(data, "c"), tui.MatchesKey(data, "ctrl+x"):
		b.emit(TreeActionCopy)
	case tui.MatchesKey(data, "d"):
		b.emitIf(TreeActionDelete, func(node TreeBrowserNode) bool { return node.Deletable })
	case tui.MatchesKey(data, "r"):
		b.emitIf(TreeActionRename, func(node TreeBrowserNode) bool { return node.Renamable })
	}
}

func isPrintableInput(data string) bool {
	if data == "" {
		return false
	}
	for _, r := range data {
		if r < 0x20 || r == 0x7F {
			return false
		}
	}
	return true
}
