package packages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const schemaVersion = 0

const ManifestFilename = "smidja.json"

const ReceiptFilename = ".receipt.json"

var packageIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

var contentKinds = map[string]bool{
	"skills":  true,
	"agents":  true,
	"prompts": true,
	"config":  true,
}

type Manifest struct {
	SchemaVersion  int               `json:"schemaVersion"`
	ID             string            `json:"id"`
	Version        string            `json:"version"`
	Owner          string            `json:"owner"`
	Repo           string            `json:"repo"`
	Description    string            `json:"description,omitempty"`
	Contents       map[string]string `json:"contents,omitempty"`
	Depends        []Dependency      `json:"depends,omitempty"`
	MinimumHarness string            `json:"minimumHarness"`
	Files          []FileEntry       `json:"files"`
}

type Dependency struct {
	ID             string `json:"id"`
	Owner          string `json:"owner"`
	Repo           string `json:"repo"`
	MinimumVersion string `json:"minimumVersion,omitempty"`
	ExactVersion   string `json:"exactVersion,omitempty"`
}

type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func Parse(data []byte) (Manifest, error) {
	if err := checkManifestJSON(data); err != nil {
		return Manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("packages: manifest: %w", err)
	}
	return m, nil
}

func checkManifestJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("packages: manifest: invalid JSON: %w", err)
	}
	if tok != json.Delim('{') {
		return errors.New("packages: manifest: top-level value must be an object")
	}
	if err := walkJSONObject(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("packages: manifest: trailing data after JSON object")
		}
		return fmt.Errorf("packages: manifest: invalid JSON: %w", err)
	}
	return nil
}

func walkJSONObject(dec *json.Decoder) error {
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("packages: manifest: invalid JSON: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("packages: manifest: invalid object key")
		}
		if seen[key] {
			return fmt.Errorf("packages: manifest: duplicate field %q", key)
		}
		seen[key] = true
		if err := walkJSONValue(dec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("packages: manifest: invalid JSON: %w", err)
	}
	return nil
}

func walkJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("packages: manifest: invalid JSON: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return walkJSONObject(dec)
	case '[':
		for dec.More() {
			if err := walkJSONValue(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("packages: manifest: invalid JSON: %w", err)
		}
		return nil
	default:
		return errors.New("packages: manifest: invalid JSON")
	}
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != schemaVersion {
		return fmt.Errorf("packages: manifest: schemaVersion %d, want %d", m.SchemaVersion, schemaVersion)
	}
	if !packageIDPattern.MatchString(m.ID) {
		return fmt.Errorf("packages: manifest: id %q must match ^[a-z0-9-]{1,64}$", m.ID)
	}
	if !isCanonicalVersion(m.Version) {
		return fmt.Errorf("packages: manifest: version %q must be canonical vMAJOR.MINOR.PATCH", m.Version)
	}
	if !isCanonicalVersion(m.MinimumHarness) {
		return fmt.Errorf("packages: manifest: minimumHarness %q must be canonical vMAJOR.MINOR.PATCH", m.MinimumHarness)
	}
	if m.Owner == "" || m.Repo == "" {
		return errors.New("packages: manifest: owner and repo are required")
	}
	if utf8.RuneCountInString(m.Description) > 200 {
		return errors.New("packages: manifest: description exceeds 200 characters")
	}
	roots := make([]string, 0, len(m.Contents))
	for kind, rootPath := range m.Contents {
		if !contentKinds[kind] {
			return fmt.Errorf("packages: manifest: unknown content kind %q", kind)
		}
		if !cleanRelativePath(rootPath) {
			return fmt.Errorf("packages: manifest: content %s root %q must be a clean relative path", kind, rootPath)
		}
		roots = append(roots, rootPath)
	}
	sort.Strings(roots)
	for i, a := range roots {
		for _, b := range roots[i+1:] {
			if rootsOverlap(a, b) {
				return fmt.Errorf("packages: manifest: content roots %q and %q overlap", a, b)
			}
		}
	}
	seenDeps := map[string]bool{}
	for _, d := range m.Depends {
		if !packageIDPattern.MatchString(d.ID) {
			return fmt.Errorf("packages: manifest: dependency id %q must match ^[a-z0-9-]{1,64}$", d.ID)
		}
		if d.Owner == "" || d.Repo == "" {
			return fmt.Errorf("packages: manifest: dependency %s needs owner and repo", d.ID)
		}
		if d.MinimumVersion != "" && d.ExactVersion != "" {
			return fmt.Errorf("packages: manifest: dependency %s has both minimumVersion and exactVersion", d.ID)
		}
		if d.MinimumVersion == "" && d.ExactVersion == "" {
			return fmt.Errorf("packages: manifest: dependency %s needs exactly one of minimumVersion or exactVersion", d.ID)
		}
		constraint := d.MinimumVersion
		if constraint == "" {
			constraint = d.ExactVersion
		}
		if !isCanonicalVersion(constraint) {
			return fmt.Errorf("packages: manifest: dependency %s version %q must be canonical vMAJOR.MINOR.PATCH", d.ID, constraint)
		}
		if d.ID == m.ID {
			return fmt.Errorf("packages: manifest: dependency %s references the package itself", d.ID)
		}
		if seenDeps[d.ID] {
			return fmt.Errorf("packages: manifest: duplicate dependency %s", d.ID)
		}
		seenDeps[d.ID] = true
	}
	prev := ""
	for i, f := range m.Files {
		if !cleanRelativePath(f.Path) {
			return fmt.Errorf("packages: manifest: file path %q must be a clean relative path", f.Path)
		}
		if !underAnyRoot(f.Path, roots) {
			return fmt.Errorf("packages: manifest: file %s is not under a content root", f.Path)
		}
		if !sha256Pattern.MatchString(f.SHA256) {
			return fmt.Errorf("packages: manifest: file %s sha256 %q must be 64 hex characters", f.Path, f.SHA256)
		}
		if f.Size <= 0 {
			return fmt.Errorf("packages: manifest: file %s size must be positive", f.Path)
		}
		if i > 0 && f.Path <= prev {
			return errors.New("packages: manifest: files must be sorted and unique by path")
		}
		prev = f.Path
	}
	return nil
}

func cleanRelativePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') {
		return false
	}
	if filepath.ToSlash(filepath.Clean(p)) != p {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func rootsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func underAnyRoot(path string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}
