package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxArtifactBytes = 100 * 1024

const MaxInstructionsBytes = 64 * 1024

type Tier string

const (
	TierBundle    Tier = "bundle"
	TierWorkspace Tier = "workspace"
	TierUser      Tier = "user"
	TierPackages  Tier = "packages"
	TierCore      Tier = "core"
)

var tierOrder = [...]Tier{TierBundle, TierWorkspace, TierUser, TierPackages, TierCore}

type SkillRef struct {
	Package string
	Name    string
	Path    string
	Content string
	Tier    Tier
	Origin  string
}

type AgentRef struct {
	Package string
	Name    string
	Path    string
	Content string
	Tier    Tier
	Origin  string
}

type PromptRef struct {
	Package string
	Name    string
	Path    string
	Content string
	Tier    Tier
	Origin  string
}

type Options struct {
	BundleID       string
	BundleFS       fs.FS
	WorkspaceDir   string
	UserHome       string
	PackagesDirs   []string
	TrustWorkspace bool
}

type Snapshot struct {
	Skills  map[string]SkillRef
	Agents  map[string]AgentRef
	Prompts map[string]PromptRef

	fingerprint string
}

func Load(opts Options) (Snapshot, error) {
	skills, err := resolve(opts, "skills", skillFromItem)
	if err != nil {
		return Snapshot{}, err
	}
	agents, err := resolve(opts, "agents", agentFromItem)
	if err != nil {
		return Snapshot{}, err
	}
	prompts, err := resolve(opts, "prompts", promptFromItem)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Skills: skills, Agents: agents, Prompts: prompts}
	s.fingerprint = fingerprintOf(s)
	return s, nil
}

func (s Snapshot) Fingerprint() string {
	if s.fingerprint == "" {
		return fingerprintOf(s)
	}
	return s.fingerprint
}

type itemRef struct {
	Package string
	Name    string
	Path    string
	Content string
	Tier    Tier
	Origin  string
}

func skillFromItem(r itemRef) SkillRef {
	return SkillRef{Package: r.Package, Name: r.Name, Path: r.Path, Content: r.Content, Tier: r.Tier, Origin: r.Origin}
}

func agentFromItem(r itemRef) AgentRef {
	return AgentRef{Package: r.Package, Name: r.Name, Path: r.Path, Content: r.Content, Tier: r.Tier, Origin: r.Origin}
}

func promptFromItem(r itemRef) PromptRef {
	return PromptRef{Package: r.Package, Name: r.Name, Path: r.Path, Content: r.Content, Tier: r.Tier, Origin: r.Origin}
}

func resolve[T any](opts Options, kind string, build func(itemRef) T) (map[string]T, error) {
	byKey := make(map[string]itemRef)
	for _, tier := range tierOrder {
		for _, src := range sourcesFor(opts, kind, tier) {
			if err := addSource(byKey, src); err != nil {
				return nil, err
			}
		}
	}
	out := make(map[string]T, len(byKey))
	for key, r := range byKey {
		out[key] = build(r)
	}
	return out, nil
}

func addSource(byKey map[string]itemRef, src source) error {
	if src.root == nil {
		return nil
	}
	return fs.WalkDir(src.root, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("content: %s: symlink %s rejected", src.pkg, p)
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		name := strings.TrimSuffix(p, ".md")
		if err := validName(name); err != nil {
			return err
		}
		data, err := fs.ReadFile(src.root, p)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("content: %s/%s: content is not valid utf-8", src.pkg, name)
		}
		if len(data) > MaxArtifactBytes {
			return fmt.Errorf("content: %s/%s: %d bytes exceeds the %d byte cap", src.pkg, name, len(data), MaxArtifactBytes)
		}
		key := src.pkg + "/" + name
		if existing, ok := byKey[key]; ok && tierRank(existing.Tier) > tierRank(src.tier) {
			return nil
		}
		byKey[key] = itemRef{
			Package: src.pkg,
			Name:    name,
			Path:    p,
			Content: string(data),
			Tier:    src.tier,
			Origin:  src.origin,
		}
		return nil
	})
}

func tierRank(t Tier) int {
	for i, tier := range tierOrder {
		if tier == t {
			return len(tierOrder) - i
		}
	}
	return 0
}

func validName(name string) error {
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("content: invalid name %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return fmt.Errorf("content: invalid name %q", name)
		}
	}
	return nil
}

func fingerprintOf(s Snapshot) string {
	var lines []string
	for key, ref := range s.Skills {
		lines = append(lines, "skills\t"+key+"\t"+string(ref.Tier)+"\t"+ref.Origin+"\t"+ref.Content)
	}
	for key, ref := range s.Agents {
		lines = append(lines, "agents\t"+key+"\t"+string(ref.Tier)+"\t"+ref.Origin+"\t"+ref.Content)
	}
	for key, ref := range s.Prompts {
		lines = append(lines, "prompts\t"+key+"\t"+string(ref.Tier)+"\t"+ref.Origin+"\t"+ref.Content)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
