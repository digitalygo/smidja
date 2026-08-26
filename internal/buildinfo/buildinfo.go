package buildinfo

import (
	"encoding/json"
	"fmt"
)

var (
	smidjaOrigin  = "github.com/digitalygo/smidja"
	smidjaVersion = "dev"
	smidjaCommit  = "none"
)

type Info struct {
	Origin  string
	Version string
	Commit  string
}

func Current() Info {
	return Info{Origin: smidjaOrigin, Version: smidjaVersion, Commit: smidjaCommit}
}

func (i Info) JSON() string {
	b, err := json.Marshal(map[string]string{
		"origin":  i.Origin,
		"version": i.Version,
		"commit":  i.Commit,
	})
	if err != nil {
		return fmt.Sprintf(`{"commit":%q,"origin":%q,"version":%q}`, i.Commit, i.Origin, i.Version)
	}
	return string(b)
}
