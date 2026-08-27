package content

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type InstructionsOptions struct {
	WorkspaceRoot string
	UserHome      string
	MaxBytes      int64
}

type Instructions struct {
	Project string
	Global  string
}

func DiscoverInstructions(cwd string, opts InstructionsOptions) (Instructions, error) {
	max := opts.MaxBytes
	if max <= 0 {
		max = MaxInstructionsBytes
	}
	instr := Instructions{}
	if p, ok := findProjectInstructions(cwd, opts.WorkspaceRoot); ok {
		content, err := readBounded(p, max)
		if err != nil {
			return Instructions{}, err
		}
		instr.Project = content
	}
	if opts.UserHome != "" {
		global := filepath.Join(opts.UserHome, ".smidja", "AGENTS.md")
		if content, err := readBounded(global, max); err == nil {
			instr.Global = content
		}
	}
	return instr, nil
}

func (i Instructions) Suffix() string {
	var sections []string
	if i.Project != "" {
		sections = append(sections, "[project instructions]\n\n"+i.Project)
	}
	if i.Global != "" {
		sections = append(sections, "[user instructions]\n\n"+i.Global)
	}
	return strings.Join(sections, "\n\n")
}

func findProjectInstructions(cwd, workspaceRoot string) (string, bool) {
	cur, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	bound := filepath.Clean(workspaceRoot)
	for {
		p := filepath.Join(cur, "AGENTS.md")
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p, true
		}
		if isSearchBound(cur, bound) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

func isSearchBound(cur, bound string) bool {
	if bound != "" && bound != "." && filepath.Clean(cur) == bound {
		return true
	}
	if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
		return true
	}
	return false
}

func readBounded(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > max {
		data = data[:max]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	return string(data), nil
}
