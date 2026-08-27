package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/gateway"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/summary"
)

const runtimeOrderingVersion = 1

type gatewayRunnerConfig struct {
	cfg                *config.Config
	providerID         string
	model              string
	system             string
	home               string
	store              *session.Store
	bindings           *bindingStore
	workspace          func(chatKey string) string
	client             agent.Client
	tools              []agent.Tool
	catalog            *extensions.ToolCatalog
	hooks              agent.HookDispatcher
	preparer           *contextPreparerAdapter
	retry              retryFunc
	isOverflow         func(string) bool
	detector           agent.LoopDetector
	contentFingerprint func(root string) string
	showThinking       bool
	stdout             io.Writer
	stderr             io.Writer
}

type gatewayRunner struct {
	cfg                *config.Config
	providerID         string
	model              string
	system             string
	home               string
	store              *session.Store
	bindings           *bindingStore
	workspace          func(chatKey string) string
	client             agent.Client
	tools              []agent.Tool
	catalog            *extensions.ToolCatalog
	hooks              agent.HookDispatcher
	preparer           *contextPreparerAdapter
	retry              retryFunc
	isOverflow         func(string) bool
	detector           agent.LoopDetector
	contentFingerprint func(root string) string
	showThinking       bool
	stdout             io.Writer
	stderr             io.Writer
}

func newGatewayRunner(cfg gatewayRunnerConfig) *gatewayRunner {
	if cfg.providerID == "" {
		cfg.providerID = "openrouter"
	}
	if cfg.model == "" {
		cfg.model = cfg.cfg.Model
	}
	if cfg.system == "" {
		cfg.system = defaultSystemPrompt
	}
	if cfg.workspace == nil {
		cfg.workspace = func(string) string { return cfg.cfg.WorkspaceRoot }
	}
	if cfg.contentFingerprint == nil {
		cfg.contentFingerprint = func(string) string { return "" }
	}
	if cfg.stdout == nil {
		cfg.stdout = io.Discard
	}
	if cfg.stderr == nil {
		cfg.stderr = io.Discard
	}
	return &gatewayRunner{
		cfg:                cfg.cfg,
		providerID:         cfg.providerID,
		model:              cfg.model,
		system:             cfg.system,
		home:               cfg.home,
		store:              cfg.store,
		bindings:           cfg.bindings,
		workspace:          cfg.workspace,
		client:             cfg.client,
		tools:              cfg.tools,
		catalog:            cfg.catalog,
		hooks:              cfg.hooks,
		preparer:           cfg.preparer,
		retry:              cfg.retry,
		isOverflow:         cfg.isOverflow,
		detector:           cfg.detector,
		contentFingerprint: cfg.contentFingerprint,
		showThinking:       cfg.showThinking,
		stdout:             cfg.stdout,
		stderr:             cfg.stderr,
	}
}

func (r *gatewayRunner) Run(ctx context.Context, work gateway.WorkItem) (gateway.RunResult, error) {
	res := gateway.RunResult{}
	sess, existed, err := r.openSession(work)
	if err != nil {
		return res, err
	}
	defer sess.Close()
	res.SessionID = sess.ID()

	root := r.workspace(work.Key)
	sysPrompt := r.systemPrompt(root)

	history, entryIDs, priorEntries, err := r.loadContext(sess, existed)
	if err != nil {
		return res, err
	}
	if len(priorEntries) > 0 && hasMessageEntries(priorEntries) {
		res.Summary = renderResumeSummary(summary.Build(entriesAsEntryish(priorEntries), summary.Options{}))
	}

	cur := currentRuntimeProfile(r.cfg, r.providerID, sysPrompt, r.toolSHA(), root)
	reset, err := syncRuntimeProfile(sess, cur, func() string { return r.contentFingerprint(root) })
	if err != nil {
		return res, err
	}
	res.ProfileReset = reset

	out := &bytes.Buffer{}
	var sink io.Writer = out
	if r.stdout != nil {
		sink = io.MultiWriter(out, r.stdout)
	}
	outWriter := &trailingWriter{w: sink}
	rd := &runDeps{
		model:        r.model,
		system:       sysPrompt,
		showThinking: r.showThinking,
		sessionPath:  sess.Path(),
		client:       r.client,
		tools:        r.tools,
		recorder:     &sessionRecorder{sess},
		stdout:       outWriter,
		stderr:       r.stderr,
		preparer:     r.preparer,
		hooks:        r.hooks,
		retry:        r.retry,
		isOverflow:   r.isOverflow,
		detector:     r.detector,
		catalog:      r.catalog,
	}
	deps := loopDeps(rd, outWriter)
	deps.SessionEntryIDs = entryIDs
	if _, err := runTurn(ctx, rd, deps, history, work.Text); err != nil {
		return res, err
	}
	if perr := rd.persistCompactions(); perr != nil {
		return res, perr
	}
	res.Text = strings.TrimRight(out.String(), "\n")
	return res, nil
}

func (r *gatewayRunner) openSession(work gateway.WorkItem) (*session.Session, bool, error) {
	if work.SessionPath != "" {
		if sess, err := r.store.Open(work.SessionPath, session.OpenOptions{Strict: true}); err == nil {
			return sess, true, nil
		}
	}
	root := r.workspace(work.Key)
	sess, err := r.store.Create(root)
	if err != nil {
		return nil, false, err
	}
	if work.Key != "" {
		if err := r.bindings.set(work.Key, sess.Path()); err != nil {
			sess.Close()
			return nil, false, err
		}
	}
	return sess, false, nil
}

func (r *gatewayRunner) loadContext(sess *session.Session, existed bool) ([]*agent.Message, []string, []session.Entry, error) {
	if !existed {
		return nil, nil, nil, nil
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		return nil, nil, nil, err
	}
	prior := loader.Entries()
	branch, err := loader.BuildContextEntries()
	if err != nil {
		return nil, nil, nil, err
	}
	var history []*agent.Message
	var entryIDs []string
	for _, e := range branch {
		me, ok := e.(*session.MessageEntry)
		if !ok {
			continue
		}
		msg, err := me.DecodeMessage()
		if err != nil {
			continue
		}
		history = append(history, msg)
		entryIDs = append(entryIDs, session.EntryID(e))
	}
	return history, entryIDs, prior, nil
}

func (r *gatewayRunner) systemPrompt(root string) string {
	p := r.system
	if instr, err := content.DiscoverInstructions(root, content.InstructionsOptions{
		WorkspaceRoot: root,
		UserHome:      r.home,
	}); err == nil {
		if suffix := instr.Suffix(); suffix != "" {
			p = p + "\n\n" + suffix
		}
	}
	return p
}

func (r *gatewayRunner) toolSHA() string {
	return toolsetFingerprint(r.catalog, r.tools)
}

func currentRuntimeProfile(cfg *config.Config, providerID, systemPrompt, toolSHA, affinityRoot string) session.CurrentProfile {
	return session.CurrentProfile{
		ProviderID:                     providerID,
		ModelID:                        cfg.Model,
		SystemPromptSHA256:             sha256HexString(systemPrompt),
		ToolSchemasCanonicalJSONSHA256: toolSHA,
		OrderingVersion:                runtimeOrderingVersion,
		AffinityKey:                    "workspace:" + affinityRoot,
	}
}

func syncRuntimeProfile(sess *session.Session, cur session.CurrentProfile, contentFingerprint func() string) (bool, error) {
	if contentFingerprint != nil {
		cur.ContentFingerprint = contentFingerprint()
	}
	persisted, ok := sess.RuntimeProfile()
	if !ok {
		_, err := sess.PersistRuntimeProfile(cur, nil)
		return false, err
	}
	if persisted.Matches(cur) {
		return false, nil
	}
	_, err := sess.ResetRuntimeProfile(cur, nil)
	return true, err
}

func sha256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func toolsetFingerprint(catalog agent.ToolCatalog, tools []agent.Tool) string {
	var list []agent.Tool
	if catalog != nil {
		list = catalog.Tools()
	} else {
		list = tools
	}
	type toolSpec struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
	}
	specs := make([]toolSpec, 0, len(list))
	for _, t := range list {
		if t == nil {
			continue
		}
		specs = append(specs, toolSpec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	b, err := json.Marshal(specs)
	if err != nil {
		return ""
	}
	return sha256HexString(string(b))
}

func hasMessageEntries(entries []session.Entry) bool {
	for _, e := range entries {
		if e.EntryType() == session.EntryTypeMessage {
			return true
		}
	}
	return false
}

func entriesAsEntryish(entries []session.Entry) []summary.Entryish {
	out := make([]summary.Entryish, len(entries))
	for i, e := range entries {
		out[i] = e
	}
	return out
}

func renderResumeSummary(d summary.Digest) string {
	var b strings.Builder
	b.WriteString("Resumed session")
	if d.ShortID != "" {
		b.WriteString(" " + d.ShortID)
	}
	if d.Workspace != "" {
		b.WriteString(" in " + d.Workspace)
	}
	b.WriteString(".")
	if len(d.LastIntents) > 0 {
		b.WriteString("\nRecent requests:")
		for _, in := range d.LastIntents {
			b.WriteString("\n- " + in)
		}
	}
	if d.LastResponseExcerpt != "" {
		b.WriteString("\nLast response: " + d.LastResponseExcerpt)
	}
	return b.String()
}

var _ gateway.TurnRunner = (*gatewayRunner)(nil)
