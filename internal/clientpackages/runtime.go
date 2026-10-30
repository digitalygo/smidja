package clientpackages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type Config struct {
	Store                *managerpackages.Store
	Base                 *machinetools.Service
	Environment          []string
	LookupEnv            func(string) string
	ProtectedEnvironment []string
}
type Runtime struct {
	cfg    Config
	runner *extensiontools.Runner
	mu     sync.RWMutex
	routes map[string]localTool
}
type localTool struct {
	TimeoutSeconds int
	Revision       managerpackages.Revision
	Path           string
	ID             string
}
type toolDocument struct {
	ID string `json:"id"`
	privateconfig.Extension
}
type teamDocument struct {
	ID string `json:"id"`
	privateconfig.Team
}

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func New(cfg Config) (*Runtime, error) {
	if cfg.Store == nil {
		return nil, errors.New("package store is required")
	}
	if cfg.Base == nil {
		cfg.Base = machinetools.New(machinetools.Config{Environment: cfg.Environment})
	}
	cfg.Environment = append([]string(nil), cfg.Environment...)
	cfg.ProtectedEnvironment = append([]string(nil), cfg.ProtectedEnvironment...)
	return &Runtime{cfg: cfg, runner: extensiontools.New(cfg.Base, cfg.Environment), routes: map[string]localTool{}}, nil
}
func Route(revision managerpackages.Revision, id string) string {
	return "package:" + revision.ID + "@" + revision.Version + "+" + revision.Digest + ":" + id
}
func packageRef(revision managerpackages.Revision) machineclient.PackageRevision {
	return machineclient.PackageRevision{ID: revision.ID, Version: revision.Version, Digest: revision.Digest}
}
func (r *Runtime) loadTool(revision managerpackages.Revision, path string) (toolDocument, error) {
	data, err := r.cfg.Store.ReadFile(revision, path)
	if err != nil {
		return toolDocument{}, err
	}
	var document toolDocument
	if err = json.Unmarshal(data, &document); err != nil {
		return document, err
	}
	if _, err := privateconfig.ExtensionTimeout(document.TimeoutSeconds); err != nil {
		return document, err
	}
	var schema map[string]json.RawMessage
	if !strings.HasPrefix(document.ID, revision.ID+"/") || document.Name == "" || len(document.Command) == 0 || strings.TrimSpace(document.Command[0]) == "" || json.Unmarshal(document.Parameters, &schema) != nil || schema == nil {
		return document, errors.New("invalid installed tool definition")
	}
	if document.ClientID != "" && document.ClientID != "manager" {
		return document, errors.New("package tools bind to the installation, not a preselected client")
	}
	for name, reference := range document.Environment {
		if !envName.MatchString(name) || !envName.MatchString(reference) {
			return document, errors.New("tool environment must contain variable references only")
		}
	}
	return document, nil
}
func (r *Runtime) Inventory(ctx context.Context) (machineclient.Inventory, error) {
	result := machineclient.Inventory{OS: runtime.GOOS, Arch: runtime.GOARCH, Tools: []string{}, PackageTimeouts: map[string]int{}, PackageErrors: map[string]string{}}
	for _, definition := range machinetools.Definitions() {
		result.Tools = append(result.Tools, definition.Name)
	}
	next := map[string]localTool{}
	retained, retainedErr := r.cfg.Store.Retained()
	if retainedErr != nil {
		result.PackageErrors["retained"] = retainedErr.Error()
	}
	for _, revision := range retained {
		if _, err := r.cfg.Store.Resolve(revision); err != nil {
			result.PackageErrors[revision.ID+"@"+revision.Version] = err.Error()
			continue
		}
		for _, path := range revision.Manifest.Tools {
			definition, err := r.loadTool(revision, path)
			if err != nil {
				result.PackageErrors[revision.ID+"@"+revision.Version] = err.Error()
				continue
			}
			next[Route(revision, definition.ID)] = localTool{Revision: revision, Path: path, ID: definition.ID, TimeoutSeconds: definition.TimeoutSeconds}
		}
	}

	for _, revision := range r.cfg.Store.List() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Packages = append(result.Packages, packageRef(revision))
		dependencies, err := r.cfg.Store.Resolve(revision)
		if err != nil {
			result.PackageErrors[revision.ID] = err.Error()
			continue
		}
		teams := map[string]json.RawMessage{}
		pinnedTools := map[string]json.RawMessage{}
		for _, dependency := range dependencies {
			for _, path := range dependency.Manifest.Tools {
				definition, err := r.loadTool(dependency, path)
				if err != nil {
					return result, err
				}
				route := Route(dependency, definition.ID)
				next[route] = localTool{Revision: dependency, Path: path, ID: definition.ID, TimeoutSeconds: definition.TimeoutSeconds}
				encoded, err := json.Marshal(machineclient.ToolDefinition{ID: definition.ID, Route: route, Description: definition.Description, TimeoutSeconds: definition.TimeoutSeconds, Parameters: definition.Parameters, Package: packageRef(dependency)})
				if err != nil {
					return result, err
				}
				if previous, exists := pinnedTools[definition.ID]; exists && !bytes.Equal(previous, encoded) {
					return result, errors.New("workflow dependency closure contains conflicting tool revisions")
				}
				pinnedTools[definition.ID] = encoded
			}
			for _, path := range dependency.Manifest.Teams {
				definition, err := r.team(dependency, path)
				if err != nil {
					return result, err
				}
				teams[definition.ID] = definition.Definition
			}
		}
		for _, path := range revision.Manifest.Skills {
			data, err := r.cfg.Store.ReadFile(revision, path)
			if err != nil {
				return result, err
			}
			if !utf8.Valid(data) {
				return result, errors.New("package skill must be UTF-8 text")
			}
			result.SkillDefinitions = append(result.SkillDefinitions, machineclient.SkillDefinition{ID: revision.ID + "/" + path, Package: packageRef(revision), Content: string(data)})
		}

		for _, path := range revision.Manifest.Tools {
			definition, err := r.loadTool(revision, path)
			if err != nil {
				return result, err
			}
			result.ToolDefinitions = append(result.ToolDefinitions, machineclient.ToolDefinition{ID: definition.ID, Route: Route(revision, definition.ID), Description: definition.Description, TimeoutSeconds: definition.TimeoutSeconds, Parameters: definition.Parameters, Package: packageRef(revision)})
		}
		for _, path := range revision.Manifest.Teams {
			definition, err := r.team(revision, path)
			if err != nil {
				return result, err
			}
			result.TeamDefinitions = append(result.TeamDefinitions, definition)
			result.Teams = append(result.Teams, definition.ID)
		}
		for _, path := range revision.Manifest.Workflows {
			data, err := r.cfg.Store.ReadFile(revision, path)
			if err != nil {
				return result, err
			}
			var workflow managerstate.Workflow
			if err = json.Unmarshal(data, &workflow); err != nil {
				return result, err
			}
			if workflow.ID == "" || len(workflow.Phases) == 0 {
				return result, errors.New("invalid installed workflow")
			}
			workflow.Version = revision.Version + "+" + revision.Digest
			workflow.TeamDefinitions = teams
			workflow.ToolDefinitions = pinnedTools
			data, err = json.Marshal(workflow)
			if err != nil {
				return result, err
			}
			result.WorkflowDefinitions = append(result.WorkflowDefinitions, machineclient.CatalogDefinition{ID: workflow.ID, Package: packageRef(revision), Definition: data})
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for route, tool := range r.routes {
		result.PackageTimeouts[route] = tool.TimeoutSeconds
		result.Tools = append(result.Tools, route)
	}
	for route, tool := range next {
		result.PackageTimeouts[route] = tool.TimeoutSeconds
		if _, exists := r.routes[route]; !exists {
			result.Tools = append(result.Tools, route)
		}
	}
	sort.Strings(result.Tools)
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if len(encoded) > 1<<20 || len(result.Tools) > 1024 {
		return result, errors.New("package inventory exceeds client protocol limit")
	}
	for route, tool := range next {
		r.routes[route] = tool
	}
	return result, nil
}
func (r *Runtime) team(revision managerpackages.Revision, path string) (machineclient.CatalogDefinition, error) {
	data, err := r.cfg.Store.ReadFile(revision, path)
	if err != nil {
		return machineclient.CatalogDefinition{}, err
	}
	var document teamDocument
	if err = json.Unmarshal(data, &document); err != nil {
		return machineclient.CatalogDefinition{}, err
	}
	if document.ID == "" || document.Name == "" || len(document.Agents) == 0 {
		return machineclient.CatalogDefinition{}, errors.New("invalid installed team")
	}
	data, err = json.Marshal(document)
	return machineclient.CatalogDefinition{ID: document.ID, Package: packageRef(revision), Definition: data}, err
}
func (r *Runtime) Execute(ctx context.Context, name string, args json.RawMessage) machinetools.Result {
	if !strings.HasPrefix(name, "package:") {
		return r.cfg.Base.Execute(ctx, name, args)
	}
	r.mu.RLock()
	tool, exists := r.routes[name]
	r.mu.RUnlock()
	if !exists {
		return failure(errors.New("package tool revision is not locally registered"))
	}
	if _, err := r.cfg.Store.Resolve(tool.Revision); err != nil {
		return failure(err)
	}
	definition, err := r.loadTool(tool.Revision, tool.Path)
	if err != nil {
		return failure(err)
	}
	if definition.ID != tool.ID {
		return failure(errors.New("package tool identity changed"))
	}
	environment := map[string]string{}
	protectedReferences := append([]string(nil), r.cfg.ProtectedEnvironment...)
	protectedReferences = append(protectedReferences, tool.Revision.Source.TokenEnv)
	for _, installed := range r.cfg.Store.List() {
		protectedReferences = append(protectedReferences, installed.Source.TokenEnv)
	}
	for key, reference := range definition.Environment {
		for _, protected := range protectedReferences {
			if reference == protected {
				return failure(errors.New("package tool cannot access runtime authentication credentials"))
			}
		}
		if r.cfg.LookupEnv == nil {
			return failure(errors.New("package environment resolver is unavailable"))
		}
		value := r.cfg.LookupEnv(reference)
		if value == "" {
			return failure(fmt.Errorf("required package environment variable is missing: %s", reference))
		}
		environment[key] = value
	}
	for index, argument := range definition.Command {
		candidate := strings.TrimPrefix(argument, "./")
		declared := false
		for _, file := range tool.Revision.Manifest.Files {
			if file.Path == candidate {
				declared = true
				break
			}
		}
		if !declared {
			if strings.HasPrefix(argument, "./") {
				return failure(errors.New("relative command file is not part of the pinned package"))
			}
			continue
		}
		path, err := r.cfg.Store.FilePath(tool.Revision, candidate)
		if err != nil {
			return failure(err)
		}
		definition.Command[index] = path
	}
	definition.ClientID = ""
	return r.runner.WithEnvironment(environment).ExecuteDefinition(ctx, definition.Extension, args)
}
func failure(err error) machinetools.Result {
	return machinetools.Result{Code: "package_error", Error: err.Error()}
}
