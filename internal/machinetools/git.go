package machinetools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) git(ctx context.Context, a Arguments, diff bool) Result {
	path, err := absolute(a.Path)
	if err != nil {
		return failure("invalid_arguments", err)
	}
	rootResult := s.run(ctx, path, s.cfg.DefaultTimeout, "git", "-c", "core.fsmonitor=false", "rev-parse", "--show-toplevel")
	if !rootResult.OK {
		return rootResult
	}
	rootProcess := rootResult.Data.(ProcessResult)
	if rootResult.Truncated {
		return failure("too_large", errors.New("repository path exceeds output limit"))
	}
	root := strings.TrimSuffix(rootProcess.Output, "\n")
	args := []string{"-c", "core.fsmonitor=false", "-c", "core.pager=cat"}
	filters := s.run(ctx, root, s.cfg.DefaultTimeout, "git", "config", "--name-only", "--null", "--get-regexp", `^filter\..*\.(clean|smudge|process|required)$`)
	filterProcess := filters.Data.(ProcessResult)
	if filters.Truncated {
		return failure("too_large", errors.New("repository filter configuration exceeds limit"))
	}
	if !filters.OK && filterProcess.ExitCode != 1 {
		return filters
	}
	for _, key := range strings.Split(filterProcess.Output, "\x00") {
		if key == "" {
			continue
		}
		value := ""
		if strings.HasSuffix(key, ".required") {
			value = "false"
		}
		args = append(args, "-c", key+"="+value)
	}
	selected := ""
	if a.File != "" {
		selected = filepath.Clean(a.File)
		if filepath.IsAbs(selected) || selected == "." || selected == ".." || strings.HasPrefix(selected, ".."+string(filepath.Separator)) {
			return failure("invalid_arguments", errors.New("file must be a repository-relative filename"))
		}
	}
	if diff && selected != "" && !a.Staged {
		tracked := s.run(ctx, root, s.cfg.DefaultTimeout, "git", append(append([]string{}, args...), "ls-files", "--error-unmatch", "--", ":(literal)"+selected)...)
		if !tracked.OK {
			if tracked.Data.(ProcessResult).ExitCode != 1 {
				return tracked
			}
			info, err := os.Lstat(filepath.Join(root, selected))
			if err != nil {
				return failure("read_failed", err)
			}
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return failure("invalid_arguments", errors.New("selected file is not a regular file or symlink"))
			}
			result := s.run(ctx, root, s.cfg.DefaultTimeout, "git", append(append([]string{}, args...), "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--", "/dev/null", "./"+selected)...)
			process := result.Data.(ProcessResult)
			if process.ExitCode == 1 && !process.TimedOut {
				result.OK = true
				result.Error = ""
				result.Code = ""
			}
			if result.OK {
				result.Data = GitResult{Root: root, Output: process.Output}
			}
			return result
		}
	}
	if diff {
		args = append(args, "diff", "--no-ext-diff", "--no-textconv", "--no-color")
		if a.Staged {
			args = append(args, "--cached")
		}
		args = append(args, "--")
		if selected != "" {
			args = append(args, ":(literal)"+selected)
		}
	} else {
		args = append(args, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	}
	result := s.run(ctx, root, s.cfg.DefaultTimeout, "git", args...)
	if result.OK {
		data := GitResult{Root: root, Output: result.Data.(ProcessResult).Output, Staged: a.Staged}
		if !diff {
			data.Files = parseGitFiles(data.Output)
			branch := s.run(ctx, root, s.cfg.DefaultTimeout, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
			if branch.OK && !branch.Truncated {
				data.Branch = strings.TrimSuffix(branch.Data.(ProcessResult).Output, "\n")
			} else if !branch.OK && branch.Data.(ProcessResult).ExitCode == 1 {
				data.Detached = true
			} else if branch.Truncated {
				return failure("too_large", errors.New("branch name exceeds output limit"))
			} else {
				return branch
			}
		}
		result.Data = data
	}
	return result
}

type GitFile struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"original_path,omitempty"`
	IndexStatus    string `json:"index_status"`
	WorktreeStatus string `json:"worktree_status"`
}

func parseGitFiles(output string) []GitFile {
	files := []GitFile{}
	records := strings.Split(output, "\x00")
	for i := 0; i < len(records)-1; i++ {
		entry := records[i]
		if len(entry) < 4 || entry[2] != ' ' {
			continue
		}
		file := GitFile{Path: entry[3:], IndexStatus: entry[:1], WorktreeStatus: entry[1:2]}
		if strings.ContainsAny(entry[:2], "RC") {
			i++
			if i >= len(records)-1 {
				break
			}
			file.OriginalPath = records[i]
		}
		files = append(files, file)
	}
	return files
}
