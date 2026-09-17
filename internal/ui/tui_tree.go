package ui

import (
	"context"
	"encoding/base64"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type TreeBrowserResult struct {
	Action         string
	EntryID        string
	Node           interactive.TreeBrowserNode
	Filter         string
	HideTimestamps bool
}

func (r *Runner) ShowTreeBrowser(ctx context.Context, options interactive.TreeBrowserOptions) (TreeBrowserResult, error) {
	if !r.Active() {
		return TreeBrowserResult{}, sdk.ErrModeUnsupported
	}
	var mu sync.Mutex
	var emitted interactive.TreeBrowserAction
	emittedOK := false
	_, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewTreeBrowser(options, interactive.NewDialogTheme(r.surface.Theme()),
			func(action interactive.TreeBrowserAction) {
				if action.Kind == interactive.TreeActionCopy {
					r.copyText(action.Node.Preview)
					return
				}
				mu.Lock()
				emitted = action
				emittedOK = true
				mu.Unlock()
				emit(DialogResult{Value: action.Kind, OK: true})
			})
	})
	if err != nil {
		return TreeBrowserResult{}, err
	}
	result := TreeBrowserResult{}
	mu.Lock()
	if emittedOK {
		result = TreeBrowserResult{Action: emitted.Kind, EntryID: emitted.EntryID, Node: emitted.Node, Filter: emitted.Filter, HideTimestamps: emitted.HideTimestamps}
	}
	mu.Unlock()
	return result, nil
}

func (r *Runner) CopyToClipboard(text string) error {
	if !r.Active() {
		return sdk.ErrModeUnsupported
	}
	r.copyText(text)
	return nil
}

func (r *Runner) copyText(text string) {
	if r == nil || text == "" {
		return
	}
	terminal := r.Terminal()
	if terminal == nil {
		return
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	terminal.Write(tui.OSC52Clipboard(encoded))
}
