package buildinfo

import "testing"

func TestCurrentDefaults(t *testing.T) {
	got := Current()
	want := Info{Origin: "github.com/digitalygo/smidja", Version: "dev", Commit: "none"}
	if got != want {
		t.Errorf("Current() = %+v, want %+v", got, want)
	}
}

func TestCurrentReflectsLinkTimeVariables(t *testing.T) {
	old := Current()
	t.Cleanup(func() {
		smidjaOrigin, smidjaVersion, smidjaCommit = old.Origin, old.Version, old.Commit
	})
	smidjaOrigin = "github.com/acme/tool"
	smidjaVersion = "v1.2.3"
	smidjaCommit = "abc1234"

	got := Current()
	want := Info{Origin: "github.com/acme/tool", Version: "v1.2.3", Commit: "abc1234"}
	if got != want {
		t.Errorf("Current() = %+v, want %+v", got, want)
	}
}

func TestInfoJSON(t *testing.T) {
	got := (Info{Origin: "github.com/acme/tool", Version: "v1.2.3", Commit: "abc"}).JSON()
	want := `{"commit":"abc","origin":"github.com/acme/tool","version":"v1.2.3"}`
	if got != want {
		t.Errorf("JSON() = %s, want %s", got, want)
	}
	got = Current().JSON()
	want = `{"commit":"none","origin":"github.com/digitalygo/smidja","version":"dev"}`
	if got != want {
		t.Errorf("JSON() = %s, want %s", got, want)
	}
}
