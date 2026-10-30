package clientextensions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
)

type Config struct {
	Dir                  string
	ClientID             string
	Base                 machineclient.Executor
	InventoryProvider    func(context.Context) (machineclient.Inventory, error)
	Environment          []string
	LookupEnv            func(string) string
	ProtectedEnvironment []string
}
type Runtime struct {
	failed   bool
	mu       sync.Mutex
	cfg      Config
	runner   *extensiontools.Runner
	lock     *os.File
	records  map[string]machineclient.ExtensionControl
	active   map[string]map[uint64]context.CancelFunc
	sequence uint64
}
type document struct {
	SchemaVersion int                                       `json:"schema_version"`
	ClientID      string                                    `json:"client_id"`
	Extensions    map[string]machineclient.ExtensionControl `json:"extensions"`
}

var privateID = regexp.MustCompile(`^private:[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func Route(id string, revision uint64) string { return id + "@" + strconv.FormatUint(revision, 10) }
func Open(cfg Config) (*Runtime, error) {
	if !filepath.IsAbs(cfg.Dir) || cfg.ClientID == "" || cfg.ClientID == "manager" {
		return nil, errors.New("absolute client extension directory and client identity required")
	}
	cfg.Environment = append([]string{}, cfg.Environment...)
	cfg.ProtectedEnvironment = append([]string{}, cfg.ProtectedEnvironment...)
	if cfg.LookupEnv == nil {
		cfg.LookupEnv = os.Getenv
	}
	if cfg.Base == nil {
		cfg.Base = machinetools.New(machinetools.Config{Environment: cfg.Environment})
	}
	if err := os.MkdirAll(cfg.Dir, 0700); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(cfg.Dir, ".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "client extension lock")
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("client extensions already open")
	}
	r := &Runtime{cfg: cfg, runner: extensiontools.New(nil, cfg.Environment), lock: lock, records: map[string]machineclient.ExtensionControl{}, active: map[string]map[uint64]context.CancelFunc{}}
	path := filepath.Join(cfg.Dir, "extensions.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		r.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		r.Close()
		return nil, errors.New("client extension state must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.Close()
		return nil, err
	}
	var doc document
	if err = decode(data, &doc); err != nil || doc.SchemaVersion != 1 || doc.ClientID != cfg.ClientID || doc.Extensions == nil {
		r.Close()
		return nil, errors.New("invalid client extension state")
	}
	for id, control := range doc.Extensions {
		if id != control.ID || r.validate(control) != nil {
			r.Close()
			return nil, errors.New("invalid persisted client extension")
		}
	}
	r.records = doc.Extensions
	return r, nil
}
func decode(data []byte, value any) error {
	if len(data) > 2<<20 {
		return errors.New("extension control exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func (r *Runtime) validate(control machineclient.ExtensionControl) error {
	if !privateID.MatchString(control.ID) || control.Revision == 0 || control.Definition.ClientID != r.cfg.ClientID {
		return errors.New("private extension identity, revision and client binding required")
	}
	for name, reference := range control.Definition.Environment {
		if !envName.MatchString(name) || !envName.MatchString(reference) {
			return errors.New("extension environment requires variable references")
		}
	}
	if control.Archived {
		return nil
	}
	definition := control.Definition
	var schema map[string]json.RawMessage
	if strings.TrimSpace(definition.Name) == "" || len(definition.Command) == 0 || strings.TrimSpace(definition.Command[0]) == "" || json.Unmarshal(definition.Parameters, &schema) != nil || schema == nil {
		return errors.New("invalid client extension definition")
	}
	return nil
}
func clone[T any](value T) T {
	data, _ := json.Marshal(value)
	var copied T
	_ = json.Unmarshal(data, &copied)
	return copied
}
func failure(code, message string) machinetools.Result {
	return machinetools.Result{Code: code, Error: message}
}
func (r *Runtime) ExecuteControl(ctx context.Context, name string, args json.RawMessage) machinetools.Result {
	if name != machineclient.ApplyExtensionControl {
		return failure("invalid_control", "Unknown client control command")
	}
	if err := ctx.Err(); err != nil {
		return failure("cancelled", err.Error())
	}
	var control machineclient.ExtensionControl
	if err := decode(args, &control); err != nil {
		return failure("invalid_control", "Malformed extension control payload")
	}
	if err := r.validate(control); err != nil {
		return failure("invalid_control", err.Error())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lock == nil || r.failed {
		return failure("closed", "Client extension store is closed")
	}
	if err := ctx.Err(); err != nil {
		return failure("cancelled", err.Error())
	}
	if old, ok := r.records[control.ID]; ok {
		if control.Revision < old.Revision {
			return failure("stale_revision", "Older extension revision rejected")
		}
		if control.Revision == old.Revision {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(control)
			if !bytes.Equal(a, b) {
				return failure("revision_conflict", "Extension revision is immutable")
			}
			return applied(control)
		}
	}
	next := clone(r.records)
	next[control.ID] = control
	if err := r.persist(next); err != nil {
		r.failed = true
		return failure("persist_failed", "Cannot durably save client extension configuration")
	}
	r.records = next
	for _, cancel := range r.active[control.ID] {
		cancel()
	}
	return applied(control)
}
func applied(control machineclient.ExtensionControl) machinetools.Result {
	return machinetools.Result{OK: true, Data: map[string]any{"id": control.ID, "revision": control.Revision, "archived": control.Archived}}
}
func (r *Runtime) persist(records map[string]machineclient.ExtensionControl) error {
	data, err := json.Marshal(document{1, r.cfg.ClientID, records})
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("client extension state exceeds size limit")
	}
	file, err := os.CreateTemp(r.cfg.Dir, ".extensions-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, filepath.Join(r.cfg.Dir, "extensions.json")); err != nil {
		return err
	}
	directory, err := os.Open(r.cfg.Dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (r *Runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lock == nil {
		return nil
	}
	for _, calls := range r.active {
		for _, cancel := range calls {
			cancel()
		}
	}
	err := r.lock.Close()
	r.lock = nil
	return err
}
func (r *Runtime) Inventory(ctx context.Context) (machineclient.Inventory, error) {
	var inventory machineclient.Inventory
	var err error
	if r.cfg.InventoryProvider != nil {
		inventory, err = r.cfg.InventoryProvider(ctx)
		if err != nil {
			return inventory, err
		}
	} else {
		inventory = machineclient.Inventory{OS: runtime.GOOS, Arch: runtime.GOARCH, Tools: []string{}}
		for _, definition := range machinetools.Definitions() {
			inventory.Tools = append(inventory.Tools, definition.Name)
		}
	}
	inventory = clone(inventory)
	inventory.ExtensionControls = true
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lock == nil || r.failed {
		return inventory, errors.New("client extension store closed")
	}
	ids := make([]string, 0, len(r.records))
	for id := range r.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		control := r.records[id]
		if control.Archived {
			continue
		}
		route := Route(id, control.Revision)
		inventory.Tools = append(inventory.Tools, route)
		inventory.ToolDefinitions = append(inventory.ToolDefinitions, machineclient.ToolDefinition{ID: id, Route: route, PrivateRevision: control.Revision, Description: control.Definition.Description, Parameters: append(json.RawMessage(nil), control.Definition.Parameters...)})
	}
	return inventory, nil
}
func (r *Runtime) Execute(ctx context.Context, name string, args json.RawMessage) machinetools.Result {
	if strings.HasPrefix(name, "__smidja_") {
		return failure("tool_unavailable", "Reserved controls are not executable tools")
	}
	if !strings.HasPrefix(name, "private:") {
		return r.cfg.Base.Execute(ctx, name, args)
	}
	r.mu.Lock()
	if r.lock == nil || r.failed {
		r.mu.Unlock()
		return failure("closed", "Client extension store is closed")
	}
	var control machineclient.ExtensionControl
	for _, candidate := range r.records {
		if !candidate.Archived && Route(candidate.ID, candidate.Revision) == name {
			control = clone(candidate)
			break
		}
	}
	if control.ID == "" {
		r.mu.Unlock()
		return failure("tool_unavailable", "Private extension route is unavailable or superseded")
	}
	callCtx, cancel := context.WithCancel(ctx)
	r.sequence++
	callID := r.sequence
	if r.active[control.ID] == nil {
		r.active[control.ID] = map[uint64]context.CancelFunc{}
	}
	r.active[control.ID][callID] = cancel
	r.mu.Unlock()
	defer func() { cancel(); r.mu.Lock(); delete(r.active[control.ID], callID); r.mu.Unlock() }()
	environment := map[string]string{}
	for key, reference := range control.Definition.Environment {
		value := r.cfg.LookupEnv(reference)
		for _, protected := range r.cfg.ProtectedEnvironment {
			if protected != "" && (reference == protected || key == protected || (value != "" && value == r.cfg.LookupEnv(protected))) {
				return failure("protected_configuration", "Extension cannot access client authentication or registry credentials")
			}
		}
		if value == "" {
			return failure("missing_configuration", fmt.Sprintf("Extension requires environment variable %s", reference))
		}
		environment[key] = value
	}
	control.Definition.ClientID = ""
	return r.runner.WithEnvironment(environment).ExecuteDefinition(callCtx, control.Definition, args)
}
