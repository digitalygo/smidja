package cli

import (
	"context"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/sdk"
)

type compactRequest struct {
	opts     sdk.CompactOptions
	signal   context.Context
	handle   *hostSessionHandle
	system   string
	messages []*agent.Message
	entryIDs []string
}

type compactFailure struct {
	opts sdk.CompactOptions
	err  error
}

type hostCompactJob struct {
	opts sdk.CompactOptions

	handle   *hostSessionHandle
	system   string
	messages []*agent.Message
	entryIDs []string

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	settled bool
}

func newHostCompactJob(opts sdk.CompactOptions) *hostCompactJob {
	return &hostCompactJob{opts: opts}
}

func (j *hostCompactJob) claimSettlement() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.settled {
		return false
	}
	j.settled = true
	return true
}

func (j *hostCompactJob) markSettled() {
	j.mu.Lock()
	j.settled = true
	j.mu.Unlock()
}

func (j *hostCompactJob) isSettled() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.settled
}

func (j *hostCompactJob) done() bool { return j.isSettled() }

func (h *hostRuntime) captureCompactRequest(signal context.Context, opts sdk.CompactOptions) compactRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return compactRequest{
		opts:     opts,
		signal:   signal,
		handle:   copyHostHandle(h.handle),
		system:   h.system,
		messages: append([]*agent.Message(nil), h.messages...),
		entryIDs: append([]string(nil), h.entryIDs...),
	}
}

func (h *hostRuntime) registerJob(job *hostCompactJob) {
	h.compactMu.Lock()
	h.compactJobs[job] = struct{}{}
	h.compactMu.Unlock()
}

func (h *hostRuntime) finishJob(job *hostCompactJob) {
	h.compactMu.Lock()
	delete(h.compactJobs, job)
	h.compactMu.Unlock()
	if job.cancel != nil {
		job.cancel()
	}
}

func (h *hostRuntime) dispatchCompactJob(req compactRequest, run func(*hostCompactJob)) bool {
	job := newHostCompactJob(req.opts)
	job.handle = req.handle
	job.system = req.system
	job.messages = req.messages
	job.entryIDs = req.entryIDs
	job.ctx, job.cancel = mergeContext(req.signal, h.runContext())
	h.registerJob(job)
	h.lifecycleMu.Lock()
	dispatch := h.lifecycle.dispatch
	h.lifecycleMu.Unlock()
	if dispatch != nil {
		if !dispatch(func() {
			defer h.finishJob(job)
			run(job)
		}) {
			h.finishJob(job)
			h.failJob(job, errHostClosed)
			return false
		}
		return true
	}
	if !h.beginCompactJob() {
		h.finishJob(job)
		h.failJob(job, errHostClosed)
		return false
	}
	go func() {
		defer h.compactWG.Done()
		defer h.finishJob(job)
		run(job)
	}()
	return true
}

func (h *hostRuntime) beginCompactJob() bool {
	h.compactMu.Lock()
	defer h.compactMu.Unlock()
	if h.compactClosed {
		return false
	}
	h.compactWG.Add(1)
	return true
}

func (h *hostRuntime) settleJobsLocked(match func(*hostCompactJob) bool, err error) []compactFailure {
	var failures []compactFailure
	for job := range h.compactJobs {
		if match != nil && !match(job) {
			continue
		}
		if job.cancel != nil {
			job.cancel()
		}
		if job.claimSettlement() {
			failures = append(failures, compactFailure{opts: job.opts, err: err})
		}
		delete(h.compactJobs, job)
	}
	return failures
}

func (h *hostRuntime) settleAllJobs(err error) []compactFailure {
	h.compactMu.Lock()
	failures := h.settleJobsLocked(nil, err)
	h.compactMu.Unlock()
	return failures
}

func (h *hostRuntime) failPendingCompacts(err error) {
	h.reportCompactFailures(h.settleAllJobs(err))
}

func (h *hostRuntime) waitCompacts() {
	h.compactMu.Lock()
	h.compactClosed = true
	h.compactMu.Unlock()
	h.compactWG.Wait()
}

func (h *hostRuntime) deferCompactFailures(failures []compactFailure) {
	if len(failures) == 0 {
		return
	}
	h.dispatchCallback(func() { h.reportCompactFailures(failures) })
}

func (h *hostRuntime) guardedCallback(fn func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			h.callbackPanics.Add(1)
		}
	}()
	fn()
}

func (h *hostRuntime) beginCallback() bool {
	h.callbackMu.Lock()
	defer h.callbackMu.Unlock()
	if h.callbackClosing {
		return false
	}
	h.callbackWG.Add(1)
	return true
}

func (h *hostRuntime) endCallback() {
	h.callbackWG.Done()
}

func (h *hostRuntime) closeCallbacks() {
	h.callbackMu.Lock()
	h.callbackClosing = true
	h.callbackMu.Unlock()
}

func (h *hostRuntime) waitCallbacks() {
	h.callbackWG.Wait()
}

func (h *hostRuntime) dispatchCallback(fn func()) {
	if fn == nil {
		return
	}
	h.lifecycleMu.Lock()
	dispatch := h.lifecycle.dispatch
	h.lifecycleMu.Unlock()
	if dispatch != nil && dispatch(func() { h.guardedCallback(fn) }) {
		return
	}
	if h.beginCallback() {
		go func() {
			defer h.endCallback()
			h.guardedCallback(fn)
		}()
		return
	}
	h.guardedCallback(fn)
}

func (h *hostRuntime) reportCompactError(opts sdk.CompactOptions, err error) {
	if opts.OnError == nil {
		return
	}
	h.guardedCallback(func() { opts.OnError(err) })
}

func (h *hostRuntime) reportCompactResult(opts sdk.CompactOptions, result sdk.CompactionResult) {
	if opts.OnComplete == nil {
		return
	}
	h.guardedCallback(func() { opts.OnComplete(result) })
}

func (h *hostRuntime) reportCompactFailures(failures []compactFailure) {
	for _, failure := range failures {
		h.reportCompactError(failure.opts, failure.err)
	}
}

func (h *hostRuntime) failJob(job *hostCompactJob, err error) bool {
	if job == nil || !job.claimSettlement() {
		return false
	}
	h.reportCompactError(job.opts, err)
	return true
}

func (h *hostRuntime) failCompact(opts sdk.CompactOptions, err error) {
	h.reportCompactError(opts, err)
}

func (h *hostRuntime) succeedCompact(opts sdk.CompactOptions, result sdk.CompactionResult) {
	h.reportCompactResult(opts, result)
}

func (h *hostRuntime) requestCompact(signal context.Context, opts sdk.CompactOptions) {
	adapter := h.currentPreparer()
	if adapter == nil {
		h.failCompact(opts, extensions.ErrUnavailable)
		return
	}
	if strings.TrimSpace(opts.CustomInstructions) != "" {
		h.failCompact(opts, errHostCompactInstructions)
		return
	}
	if signal == nil {
		signal = h.runContext()
	}
	if err := signal.Err(); err != nil {
		h.failCompact(opts, err)
		return
	}
	if h.closed.Load() {
		h.failCompact(opts, errHostClosed)
		return
	}
	h.turnMu.Lock()
	if h.turnActive {
		accepted := adapter.requestCompact(opts)
		h.turnMu.Unlock()
		if !accepted {
			h.failCompact(opts, errHostCompactPending)
		}
		return
	}
	h.turnMu.Unlock()
	if h.closed.Load() {
		h.failCompact(opts, errHostClosed)
		return
	}
	h.dispatchCompactJob(h.captureCompactRequest(signal, opts), func(job *hostCompactJob) {
		h.runIdleCompact(nil, adapter, job)
	})
}

func (h *hostRuntime) claimPendingCompact() []sdk.CompactOptions {
	h.turnMu.Lock()
	adapter := h.currentPreparer()
	var pending []sdk.CompactOptions
	if adapter != nil {
		if opts, ok := adapter.takeCompact(); ok {
			pending = append(pending, opts)
		}
	}
	h.turnMu.Unlock()
	return pending
}

func (h *hostRuntime) cancelPendingCompact() {
	pending := h.claimPendingCompact()
	failures := make([]compactFailure, 0, len(pending))
	for _, opts := range pending {
		failures = append(failures, compactFailure{opts: opts, err: errHostCompactCanceled})
	}
	h.reportCompactFailures(failures)
}

func (h *hostRuntime) schedulePendingCompactCancel() {
	pending := h.claimPendingCompact()
	if len(pending) == 0 {
		return
	}
	failures := make([]compactFailure, 0, len(pending))
	for _, opts := range pending {
		failures = append(failures, compactFailure{opts: opts, err: errHostCompactCanceled})
	}
	h.dispatchCallback(func() { h.reportCompactFailures(failures) })
}

func (h *hostRuntime) runIdleCompact(signal context.Context, adapter *contextPreparerAdapter, job *hostCompactJob) {
	if job == nil || job.done() {
		return
	}
	if job.ctx == nil {
		ctx, cancel := mergeContext(signal, h.runContext())
		job.ctx = ctx
		job.cancel = cancel
		defer cancel()
	}
	if err := job.ctx.Err(); err != nil {
		h.failJob(job, err)
		return
	}
	if job.handle == nil {
		h.failJob(job, errHostClosed)
		return
	}
	if len(job.messages) == 0 {
		h.failJob(job, errHostNothingToCompact)
		return
	}
	_, entry, err := adapter.compactNow(job.ctx, job.system, job.messages, job.entryIDs)
	if err != nil {
		h.failJob(job, err)
		return
	}
	if entry == nil {
		h.failJob(job, errHostNothingToCompact)
		return
	}
	if err := h.commitCompaction(job, entry); err != nil {
		h.failJob(job, err)
		return
	}
	h.reportCompactResult(job.opts, compactionSDKResult(entry))
}

func (h *hostRuntime) commitCompaction(job *hostCompactJob, entry *agent.CompactionEntry) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	h.compactMu.Lock()
	defer h.compactMu.Unlock()
	if job.isSettled() {
		return errHostCompactSettled
	}
	if h.closed.Load() {
		return errHostClosed
	}
	h.mu.Lock()
	current := h.handle
	h.mu.Unlock()
	if job.handle == nil || current == nil || current.generation != job.handle.generation {
		return errHostStaleSession
	}
	if job.ctx != nil {
		if err := job.ctx.Err(); err != nil {
			return err
		}
	}
	if err := persistCompaction(job.handle, entry); err != nil {
		return err
	}
	job.markSettled()
	return nil
}

func (h *hostRuntime) persistGuarded(ctx context.Context, handle *hostSessionHandle, entry *agent.CompactionEntry) error {
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	h.compactMu.Lock()
	defer h.compactMu.Unlock()
	if h.closed.Load() {
		return errHostClosed
	}
	h.mu.Lock()
	current := h.handle
	h.mu.Unlock()
	if handle == nil || current == nil || current.generation != handle.generation {
		return errHostStaleSession
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return persistCompaction(handle, entry)
}

func (h *hostRuntime) runExplicitCompact(ctx context.Context, req agent.ContextRequest, opts sdk.CompactOptions) agent.ContextResult {
	base := agent.ContextResult{Messages: req.Messages, System: req.System}
	adapter := h.currentPreparer()
	if adapter == nil {
		h.failCompact(opts, extensions.ErrUnavailable)
		return base
	}
	handle := h.snapshot()
	kept, entry, err := adapter.compactNow(ctx, req.System, req.Messages, req.EntryIDs)
	if err != nil {
		h.failCompact(opts, err)
		return base
	}
	if entry == nil {
		h.failCompact(opts, errHostNothingToCompact)
		return base
	}
	if err := h.persistGuarded(ctx, handle, entry); err != nil {
		h.failCompact(opts, err)
		return base
	}
	h.succeedCompact(opts, compactionSDKResult(entry))
	base.Messages = kept
	base.Compacted = true
	base.Compaction = entry
	return base
}

func persistCompaction(handle *hostSessionHandle, entry *agent.CompactionEntry) error {
	if entry == nil {
		return nil
	}
	if handle == nil || handle.recorder == nil {
		return errHostClosed
	}
	sink, ok := handle.recorder.(compactionSink)
	if !ok {
		return errHostClosed
	}
	return sink.appendCompaction(entry)
}

func compactionSDKResult(entry *agent.CompactionEntry) sdk.CompactionResult {
	if entry == nil {
		return sdk.CompactionResult{}
	}
	return sdk.CompactionResult{
		Summary:          string(entry.Summary),
		FirstKeptEntryID: entry.FirstKeptEntryID,
		TokensBefore:     entry.TokensBefore,
	}
}
