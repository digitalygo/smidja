package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/digitalygo/smidja/internal/agent"
)

func runScheduledHostTurn(ctx context.Context, d *runDeps, turn hostScheduledTurn) {
	if d == nil || d.host == nil || !d.host.scheduledTurnCurrent(turn) {
		return
	}
	history, entryIDs := d.host.modelHistorySnapshot()
	stdout := d.stdout
	if stdout == nil {
		stdout = io.Discard
	}
	out := &trailingWriter{w: stdout}
	deps := loopDeps(d, out)
	deps.SessionEntryIDs = entryIDs
	d.attachProjectedEntryIDs(deps)
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	turnCtx = withHostTurnCancel(turnCtx, cancel)
	var (
		updated []*agent.Message
		err     error
	)
	if turn.external {
		updated, err = runTurn(turnCtx, d, deps, history, turn.text, turn)
	} else {
		updated, err = runContinuation(turnCtx, d, deps, history, turn)
	}
	if updated != nil {
		d.host.setMessages(updated)
	}
	if err != nil && d.stderr != nil {
		fmt.Fprintf(d.stderr, "smidja: scheduled message: %v\n", err)
	}
	if !out.endsWithNewline() {
		fmt.Fprintln(out.w)
	}
}
