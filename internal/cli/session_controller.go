package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/subagent"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type sessionLoadMode int

const (
	sessionModeNew sessionLoadMode = iota
	sessionModeResume
	sessionModeFork
)

type candidateOrigin int

const (
	candidateCallerOwned candidateOrigin = iota
	candidateOpened
	candidateCreated
)

type activeSession struct {
	sess       *session.Session
	recorder   *sessionRecorder
	path       string
	history    []*agent.Message
	entryIDs   []string
	transcript []interactive.TranscriptEntry
	tree       []interactive.TreeBrowserNode
	warnings   []string
	profile    *session.RuntimeProfile
	name       string
	mode       sessionLoadMode
	origin     candidateOrigin
	identity   os.FileInfo
	loader     *session.Loader
	preparer   *contextPreparerAdapter
	detector   agent.LoopDetector
	model      string
	wireModel  string
}

type sessionBuildEnv struct {
	cfg         *config.Config
	providerID  string
	system      string
	tools       []agent.Tool
	catalog     *extensions.ToolCatalog
	modelReg    *models.Registry
	selector    subagent.Selector
	fingerprint func() string
}

type sessionController struct {
	mu       sync.Mutex
	cond     *sync.Cond
	prepMu   sync.Mutex
	store    *session.Store
	cwd      string
	prepare  func(sess *session.Session, mode sessionLoadMode) (*activeSession, error)
	apply    func(previous, next *activeSession) error
	reload   func(path string) (*session.Loader, error)
	current  *activeSession
	closed   bool
	inflight int
}

var errSessionControllerClosed = errors.New("session: controller is closed")

func newSessionController(store *session.Store, cwd string) *sessionController {
	controller := &sessionController{store: store, cwd: cwd}
	controller.cond = sync.NewCond(&controller.mu)
	return controller
}

func (c *sessionController) SetPreparer(prepare func(sess *session.Session, mode sessionLoadMode) (*activeSession, error)) {
	c.mu.Lock()
	c.prepare = prepare
	c.mu.Unlock()
}

func (c *sessionController) SetApplier(apply func(previous, next *activeSession) error) {
	c.mu.Lock()
	c.apply = apply
	c.mu.Unlock()
}

func (c *sessionController) Hold(sess *session.Session) {
	if sess == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.current = &activeSession{sess: sess, recorder: &sessionRecorder{sess}, path: sess.Path(), mode: sessionModeNew}
}

func (c *sessionController) Current() *activeSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *sessionController) Refresh() (*activeSession, error) {
	c.prepMu.Lock()
	defer c.prepMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errSessionControllerClosed
	}
	current := c.current
	reload := c.reload
	c.mu.Unlock()
	if current == nil || current.sess == nil || current.path == "" {
		return nil, errors.New("session: no active session to reload")
	}
	loader, err := c.loadForRefresh(current.path, reload)
	if err != nil {
		return nil, fmt.Errorf("session: reload %q: %w", current.path, err)
	}
	projection, err := projectSession(loader)
	if err != nil {
		return nil, fmt.Errorf("session: project %q: %w", current.path, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errSessionControllerClosed
	}
	if c.current != current {
		return nil, errors.New("session: the active session changed while reloading")
	}
	current.loader = loader
	current.history = projection.history
	current.entryIDs = projection.entryIDs
	current.transcript = projection.transcript
	current.tree = projection.tree
	current.warnings = projection.warnings
	current.name = projection.name
	return current, nil
}

func (c *sessionController) loadForRefresh(path string, reload func(string) (*session.Loader, error)) (*session.Loader, error) {
	if reload != nil {
		return reload(path)
	}
	return session.LoadWithOptions(path, session.LoadOptions{Strict: true})
}

func (c *sessionController) Adopt(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
	if sess == nil {
		return nil, errors.New("session: cannot adopt a nil session")
	}
	active, err := c.prepareSession(mode, candidateCallerOwned, func() (*session.Session, os.FileInfo, error) {
		return sess, nil, nil
	}, nil)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errSessionControllerClosed
	}
	c.current = active
	return active, nil
}

func (c *sessionController) PrepareOpen(pathOrID string) (*activeSession, error) {
	c.mu.Lock()
	store := c.store
	c.mu.Unlock()
	if store == nil {
		return nil, errors.New("session: controller is not ready")
	}
	return c.prepareSession(sessionModeResume, candidateOpened, func() (*session.Session, os.FileInfo, error) {
		sess, err := store.Open(pathOrID, session.OpenOptions{Strict: true})
		return sess, nil, err
	}, nil)
}

func (c *sessionController) PrepareNew() (*activeSession, error) {
	c.mu.Lock()
	store := c.store
	cwd := c.cwd
	c.mu.Unlock()
	if store == nil {
		return nil, errors.New("session: controller is not ready")
	}
	return c.prepareSession(sessionModeNew, candidateCreated, func() (*session.Session, os.FileInfo, error) {
		return createTrackedSession(store, cwd)
	}, nil)
}

func (c *sessionController) prepareSession(mode sessionLoadMode, origin candidateOrigin, open func() (*session.Session, os.FileInfo, error), setup func(*session.Session) error) (*activeSession, error) {
	c.prepMu.Lock()
	defer c.prepMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errSessionControllerClosed
	}
	prepare := c.prepare
	if prepare == nil {
		c.mu.Unlock()
		return nil, errors.New("session: controller is not ready")
	}
	c.inflight++
	c.mu.Unlock()
	defer c.finishPrepare()

	candidate, identity, err := open()
	if err != nil {
		return nil, err
	}
	if origin == candidateCreated && identity == nil {
		return nil, errors.Join(errors.New("session: created candidate has no file identity"), disposeCandidate(candidate, origin, nil))
	}
	if setup != nil {
		if err := setup(candidate); err != nil {
			return nil, errors.Join(err, disposeCandidate(candidate, origin, identity))
		}
	}
	active, err := prepare(candidate, mode)
	if err != nil {
		return nil, errors.Join(err, disposeCandidate(candidate, origin, identity))
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.Join(errSessionControllerClosed, disposeCandidate(candidate, origin, identity))
	}
	if active == nil {
		return nil, errors.Join(errors.New("session: preparer returned no session"), disposeCandidate(candidate, origin, identity))
	}
	active.origin = origin
	active.identity = identity
	return active, nil
}

func disposeCandidate(candidate *session.Session, origin candidateOrigin, identity os.FileInfo) error {
	if candidate == nil || origin == candidateCallerOwned {
		return nil
	}
	if origin != candidateCreated {
		return candidate.Close()
	}
	removeErr := removeCreatedCandidate(candidate.Path(), identity)
	closeErr := candidate.Close()
	return errors.Join(removeErr, closeErr)
}

func (c *sessionController) finishPrepare() {
	c.mu.Lock()
	if c.inflight > 0 {
		c.inflight--
	}
	if c.inflight == 0 {
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}

const (
	sessionProvenanceCustomType = "smidja.session.provenance"
	sessionProvenancePayload    = `{"origin":"created"}`
)

func createLockedSession(store *session.Store, cwd string) (*session.Session, error) {
	sess, _, err := createTrackedSession(store, cwd)
	return sess, err
}

func createTrackedSession(store *session.Store, cwd string) (*session.Session, os.FileInfo, error) {
	candidate, err := store.Create(cwd)
	if err != nil {
		return nil, nil, err
	}
	if err := materializeSessionProvenance(candidate); err != nil {
		return nil, nil, errors.Join(err, candidate.Close())
	}
	path := candidate.Path()
	identity, err := os.Lstat(path)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("session: inspect created session %q: %w", path, err), candidate.Close())
	}
	if identity.Mode()&os.ModeSymlink != 0 || !identity.Mode().IsRegular() {
		return nil, nil, errors.Join(
			fmt.Errorf("session: created session %q is not a plain file", path),
			removeCreatedCandidate(path, identity),
			candidate.Close(),
		)
	}
	if err := candidate.Close(); err != nil {
		return nil, nil, err
	}
	if createHookBeforeReopen != nil {
		createHookBeforeReopen(path)
	}
	opened, err := store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		return nil, nil, err
	}
	return opened, identity, nil
}

var (
	removeCreatedCandidateFile = os.Remove
	createHookBeforeReopen     func(path string)
)

func removeCreatedCandidate(path string, identity os.FileInfo) error {
	if path == "" || identity == nil {
		return errors.New("session: cannot verify the created candidate identity")
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session: inspect created candidate %q: %w", path, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return fmt.Errorf("session: refusing to remove a replaced candidate at %q", path)
	}
	if !os.SameFile(identity, current) {
		return fmt.Errorf("session: refusing to remove a replaced candidate at %q", path)
	}
	if err := removeCreatedCandidateFile(path); err != nil {
		return fmt.Errorf("session: remove created candidate %q: %w", path, err)
	}
	return nil
}

func materializeSessionProvenance(sess *session.Session) error {
	return sess.AppendEntry(&session.CustomEntry{CustomType: sessionProvenanceCustomType, Data: json.RawMessage(sessionProvenancePayload)})
}

func (c *sessionController) PrepareFork(targetID string, opts sdk.ForkOptions) (*activeSession, error) {
	if opts.Position != "" && opts.Position != "end" {
		return nil, fmt.Errorf("fork: unsupported position %q", opts.Position)
	}
	c.mu.Lock()
	store := c.store
	cwd := c.cwd
	current := c.current
	c.mu.Unlock()
	if store == nil || current == nil || current.sess == nil {
		return nil, errors.New("session: controller is not ready")
	}
	var prefix []session.Entry
	return c.prepareSession(sessionModeFork, candidateCreated, func() (*session.Session, os.FileInfo, error) {
		branch, err := forkPrefixEntries(current.sessLoader(), targetID)
		if err != nil {
			return nil, nil, err
		}
		prefix = branch
		return createTrackedSession(store, cwd)
	}, func(candidate *session.Session) error {
		return cloneSessionPrefix(candidate, prefix)
	})
}

func (a *activeSession) sessLoader() *session.Loader {
	if a == nil || a.path == "" {
		return nil
	}
	loader, err := session.LoadWithOptions(a.path, session.LoadOptions{Strict: true})
	if err != nil {
		return nil
	}
	return loader
}

func (c *sessionController) Commit(next *activeSession) error {
	return c.CommitWithContext(context.Background(), next)
}

func (c *sessionController) CommitWithContext(ctx context.Context, next *activeSession) error {
	if next == nil {
		return errors.New("session: cannot commit a nil session")
	}
	c.prepMu.Lock()
	defer c.prepMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx != nil && ctx.Err() != nil {
		return errors.Join(ctx.Err(), next.dispose())
	}
	if c.closed {
		return errors.Join(errSessionControllerClosed, next.dispose())
	}
	old := c.current
	if c.apply != nil {
		if err := c.apply(old, next); err != nil {
			return errors.Join(err, next.dispose())
		}
	}
	c.current = next
	if old != nil && old.sess != nil && old.sess != next.sess {
		old.close()
	}
	return nil
}

func (c *sessionController) Abort(next *activeSession) error {
	if next == nil {
		return nil
	}
	c.prepMu.Lock()
	defer c.prepMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	return next.dispose()
}

func (c *sessionController) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	for c.inflight > 0 {
		c.cond.Wait()
	}
	old := c.current
	c.current = nil
	c.mu.Unlock()
	if old == nil || old.sess == nil {
		return nil
	}
	return old.sess.Close()
}

func (a *activeSession) close() {
	if a == nil || a.sess == nil {
		return
	}
	a.sess.Close()
}

func (a *activeSession) dispose() error {
	if a == nil {
		return nil
	}
	return disposeCandidate(a.sess, a.origin, a.identity)
}

func buildActiveSession(env *sessionBuildEnv, rd *runDeps, sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
	var loader *session.Loader
	if mode != sessionModeNew {
		loaded, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
		if err != nil {
			return nil, err
		}
		loader = loaded
	}
	projection, err := projectSession(loader)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(rd.model)
	wire := rd.wireModelID()
	cur := currentRuntimeProfile(env.cfg, env.providerID, env.system, toolsetFingerprint(env.catalog, env.tools), env.cfg.WorkspaceRoot)
	cur.ModelID = model
	if env.cfg.Model != model {
		cur.ProviderID = env.providerID
	}
	var profileErr error
	if mode == sessionModeFork {
		_, profileErr = sess.ResetRuntimeProfile(cur, env.fingerprint)
	} else {
		_, profileErr = syncRuntimeProfile(sess, cur, env.fingerprint)
	}
	if profileErr != nil {
		return nil, profileErr
	}
	profile, _ := sess.RuntimeProfile()
	preparer, err := newModelPreparer(*env.cfg, env.modelReg, model, wire, env.selector)
	if err != nil {
		return nil, err
	}
	return &activeSession{
		sess:       sess,
		recorder:   &sessionRecorder{sess},
		path:       sess.Path(),
		history:    projection.history,
		entryIDs:   projection.entryIDs,
		transcript: projection.transcript,
		tree:       projection.tree,
		warnings:   projection.warnings,
		profile:    profile,
		name:       projection.name,
		mode:       mode,
		loader:     loader,
		preparer:   preparer,
		detector:   newLoopDetectorAdapter(loopdetector.New(loopdetector.DefaultConfig())),
		model:      model,
		wireModel:  wire,
	}, nil
}

func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
