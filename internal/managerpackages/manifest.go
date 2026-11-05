package managerpackages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/digitalygo/smidja/internal/managerstate"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const ManifestFilename = "smidja.json"
const maximumBytes = 64 << 20

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var supportedTargets = map[string]bool{"linux/amd64": true, "linux/arm64": true, "darwin/amd64": true, "darwin/arm64": true}

type Manifest struct {
	SchemaVersion  int                      `json:"schema_version"`
	ID             string                   `json:"id"`
	Version        string                   `json:"version"`
	Publisher      string                   `json:"publisher"`
	RequiresSmidja string                   `json:"requires_smidja"`
	Scopes         []string                 `json:"scopes"`
	Targets        []string                 `json:"targets"`
	Teams          []string                 `json:"teams"`
	Workflows      []string                 `json:"workflows"`
	Tools          []string                 `json:"tools"`
	Skills         []string                 `json:"skills"`
	Files          []File                   `json:"files"`
	Dependencies   []Dependency             `json:"dependencies"`
	Configuration  map[string]Configuration `json:"configuration"`
}
type Configuration struct {
	Secret bool `json:"secret"`
}
type Dependency struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type File struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

func strictJSON(data []byte, value any) error {
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
func validPath(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, "\\\x00:") && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}
func compareVersion(a, b string) (int, error) {
	if !versionPattern.MatchString(a) || !versionPattern.MatchString(b) {
		return 0, errors.New("version must be x.y.z without prerelease")
	}
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := range left {
		av, e := strconv.ParseUint(left[i], 10, 32)
		if e != nil {
			return 0, e
		}
		bv, e := strconv.ParseUint(right[i], 10, 32)
		if e != nil {
			return 0, e
		}
		if av < bv {
			return -1, nil
		}
		if av > bv {
			return 1, nil
		}
	}
	return 0, nil
}
func validateManifest(m Manifest, version, target, scope string) error {
	if m.SchemaVersion != 1 || !identifier.MatchString(m.ID) || strings.TrimSpace(m.Publisher) == "" {
		return errors.New("invalid package identity or schema")
	}
	if _, err := compareVersion(m.Version, m.Version); err != nil {
		return err
	}
	if !strings.HasPrefix(m.RequiresSmidja, ">=") {
		return errors.New("requires_smidja must use >=x.y.z")
	}
	comparison, err := compareVersion(version, strings.TrimPrefix(m.RequiresSmidja, ">="))
	if err != nil {
		return err
	}
	if comparison < 0 {
		return errors.New("package requires newer Smidja")
	}
	targetOK, scopeOK := false, false
	for _, candidate := range m.Targets {
		if !supportedTargets[candidate] {
			return fmt.Errorf("unsupported target %q", candidate)
		}
		if candidate == target {
			targetOK = true
		}
	}
	for _, candidate := range m.Scopes {
		if candidate != "manager" && candidate != "client" {
			return errors.New("invalid execution scope")
		}
		if candidate == scope {
			scopeOK = true
		}
	}
	if !targetOK || !scopeOK {
		return errors.New("package is incompatible with this target or scope")
	}
	files := map[string]bool{}
	for _, file := range m.Files {
		if !validPath(file.Path) || file.Path == ManifestFilename || files[file.Path] || !digestPattern.MatchString(file.SHA256) {
			return errors.New("invalid package file inventory")
		}
		files[file.Path] = true
	}
	references := map[string]bool{}
	for _, group := range [][]string{m.Teams, m.Workflows, m.Tools, m.Skills} {
		for _, name := range group {
			if !files[name] || references[name] {
				return errors.New("missing or repeated catalog file")
			}
			references[name] = true
		}
	}
	dependencies := map[string]bool{}
	for _, dependency := range m.Dependencies {
		if !identifier.MatchString(dependency.ID) || dependency.ID == m.ID || dependencies[dependency.ID] || !digestPattern.MatchString(dependency.Digest) {
			return errors.New("invalid dependency pin")
		}
		if _, err := compareVersion(dependency.Version, dependency.Version); err != nil {
			return err
		}
		dependencies[dependency.ID] = true
	}
	for name := range m.Configuration {
		if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(name) {
			return errors.New("invalid configuration environment name")
		}
	}
	return nil
}
func validateCatalog(m Manifest, files map[string][]byte) error {
	for _, name := range m.Workflows {
		var workflow managerstate.Workflow
		if err := json.Unmarshal(files[name], &workflow); err != nil {
			return err
		}
		if err := managerstate.ValidateWorkflowPolicy(workflow); err != nil {
			return err
		}
	}
	ids := map[string]bool{}
	for _, group := range [][]string{m.Teams, m.Workflows, m.Tools} {
		for _, name := range group {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(files[name], &object); err != nil {
				return fmt.Errorf("invalid catalog file %s: %w", name, err)
			}
			var id string
			if err := json.Unmarshal(object["id"], &id); err != nil || !strings.HasPrefix(id, m.ID+"/") || len(id) <= len(m.ID)+1 || ids[id] {
				return errors.New("catalog IDs must be unique and package namespaced")
			}
			ids[id] = true
		}
	}
	return nil
}
