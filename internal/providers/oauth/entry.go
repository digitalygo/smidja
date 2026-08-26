package oauth

import (
	"encoding/json"

	"github.com/digitalygo/smidja/internal/authstore"
)

func newEntry(access, refresh string, expires int64, extras map[string]string) (authstore.Entry, error) {
	wire := make(map[string]any, 5+len(extras))
	wire["type"] = "oauth"
	wire["access"] = access
	wire["refresh"] = refresh
	wire["expires"] = expires
	for key, value := range extras {
		wire[key] = value
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return authstore.Entry{}, err
	}
	var entry authstore.Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return authstore.Entry{}, err
	}
	return entry, nil
}
