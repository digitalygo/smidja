package prreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func TestPublishedPackageSourceMatchesBuiltin(t *testing.T) {
	root := "../../packages/pr-review"
	data, err := os.ReadFile(filepath.Join(root, "smidja.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest managerpackages.Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "smidja.pr-review" || manifest.Version != Version || len(manifest.Teams) != 4 || len(manifest.Workflows) != 1 || len(manifest.Tools) != 0 {
		t.Fatal(manifest)
	}
	for _, file := range manifest.Files {
		data, err = os.ReadFile(filepath.Join(root, file.Path))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if file.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal(file.Path, "digest mismatch")
		}
	}
	data, err = os.ReadFile(filepath.Join(root, manifest.Workflows[0]))
	if err != nil {
		t.Fatal(err)
	}
	var published managerstate.Workflow
	if err = json.Unmarshal(data, &published); err != nil {
		t.Fatal(err)
	}
	builtin := Workflow("openai/gpt-6.1-terra")
	if published.ID != builtin.ID || published.Version != builtin.Version || len(published.Phases) != 4 {
		t.Fatal(published)
	}
	for i, path := range manifest.Teams {
		data, err = os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		var team privateconfig.Team
		var expected privateconfig.Team
		if err = json.Unmarshal(data, &team); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(builtin.TeamDefinitions[builtin.Phases[i].TeamRef], &expected); err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(team)
		b, _ := json.Marshal(expected)
		if string(a) != string(b) || published.Phases[i] != builtin.Phases[i] {
			t.Fatal(path, "builtin diverged")
		}
	}
}
