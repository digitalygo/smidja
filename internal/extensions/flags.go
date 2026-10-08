package extensions

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrFlagName      = errors.New("extensions: invalid flag name")
	ErrFlagType      = errors.New("extensions: unsupported flag type")
	ErrFlagDefault   = errors.New("extensions: flag default does not match its type")
	ErrFlagReserved  = errors.New("extensions: flag name collides with a core flag")
	ErrFlagDuplicate = errors.New("extensions: flag is already registered")
)

var flagNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type FlagDeclaration struct {
	Name string

	Type string

	Description string

	Default any
}

type FlagRegistry struct {
	mu     sync.Mutex
	order  []string
	decls  map[string]FlagDeclaration
	values map[string]any
	core   map[string]struct{}
}

func NewFlagRegistry(core ...string) *FlagRegistry {
	reserved := make(map[string]struct{}, len(core))
	for _, name := range core {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			reserved[trimmed] = struct{}{}
		}
	}
	return &FlagRegistry{
		decls:  make(map[string]FlagDeclaration),
		values: make(map[string]any),
		core:   reserved,
	}
}

func (r *FlagRegistry) Register(name string, opts sdk.FlagOptions) error {
	if r == nil {
		return ErrUnavailable
	}
	name = strings.TrimSpace(name)
	if !flagNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrFlagName, name)
	}
	flagType, err := normalizeFlagType(opts.Type)
	if err != nil {
		return err
	}
	if _, taken := r.core[name]; taken {
		return fmt.Errorf("%w: %s", ErrFlagReserved, name)
	}
	def, err := normalizeFlagDefault(flagType, opts.Default)
	if err != nil {
		return fmt.Errorf("%w: %s", err, name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.decls[name]; dup {
		return fmt.Errorf("%w: %s", ErrFlagDuplicate, name)
	}
	r.decls[name] = FlagDeclaration{Name: name, Type: flagType, Description: opts.Description, Default: def}
	r.order = append(r.order, name)
	return nil
}

func normalizeFlagType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "boolean":
		return "boolean", nil
	case "string":
		return "string", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrFlagType, raw)
	}
}

func normalizeFlagDefault(flagType string, def any) (any, error) {
	switch flagType {
	case "boolean":
		switch typed := def.(type) {
		case nil:
			return false, nil
		case bool:
			return typed, nil
		default:
			return nil, ErrFlagDefault
		}
	case "string":
		switch typed := def.(type) {
		case nil:
			return "", nil
		case string:
			return typed, nil
		default:
			return nil, ErrFlagDefault
		}
	default:
		return nil, ErrFlagType
	}
}

func (r *FlagRegistry) Declarations() []FlagDeclaration {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]FlagDeclaration, 0, len(r.order))
	for _, name := range r.order {
		decl, ok := r.decls[name]
		if !ok {
			continue
		}
		out = append(out, decl)
	}
	return out
}

func (r *FlagRegistry) Apply(fs *flag.FlagSet) {
	if r == nil || fs == nil {
		return
	}
	for _, decl := range r.Declarations() {
		if fs.Lookup(decl.Name) != nil {
			continue
		}
		switch decl.Type {
		case "boolean":
			fs.Bool(decl.Name, decl.Default.(bool), decl.Description)
		case "string":
			fs.String(decl.Name, decl.Default.(string), decl.Description)
		}
	}
}

func (r *FlagRegistry) Capture(fs *flag.FlagSet) {
	if r == nil || fs == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]any, len(r.decls))
	for name, decl := range r.decls {
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		switch decl.Type {
		case "boolean":
			if value, err := strconv.ParseBool(f.Value.String()); err == nil {
				values[name] = value
			}
		case "string":
			values[name] = f.Value.String()
		}
	}
	r.values = values
}

func (r *FlagRegistry) Values() map[string]any {
	if r == nil {
		return map[string]any{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]any, len(r.values))
	for name, value := range r.values {
		out[name] = value
	}
	return out
}

type flagSnapshot struct {
	order []string
	decls map[string]FlagDeclaration
}

func (r *FlagRegistry) snapshot() flagSnapshot {
	if r == nil {
		return flagSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := flagSnapshot{
		order: append([]string(nil), r.order...),
		decls: make(map[string]FlagDeclaration, len(r.decls)),
	}
	for name, decl := range r.decls {
		snap.decls[name] = decl
	}
	return snap
}

func (r *FlagRegistry) restore(snap flagSnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.order = snap.order
	r.decls = snap.decls
	r.mu.Unlock()
}
