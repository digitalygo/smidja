package verktriggers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/digitalygo/smidja/internal/verk"
)

var ownerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

func Validate(trigger verk.Trigger) error {
	switch trigger.Type {
	case "smidr":
		return nil
	case "schedule":
		_, err := ParseSchedule(trigger.Schedule, trigger.Timezone)
		return err
	case "webhook":
		if !safeID.MatchString(trigger.SourceRef) || trigger.SourceRef == "github" || strings.HasPrefix(trigger.SourceRef, "github:") {
			return fmt.Errorf("webhook requires an explicit custom source")
		}
		return nil
	case "push", "pr", "ci":
		if len(trigger.Repositories) == 0 {
			return fmt.Errorf("repository trigger requires explicit scope")
		}
		if trigger.Scope != "specific" && trigger.Scope != "list" && trigger.Scope != "organization" {
			return fmt.Errorf("unknown repository scope")
		}
		if trigger.Scope == "specific" && len(trigger.Repositories) != 1 {
			return fmt.Errorf("specific scope requires one repository")
		}
		for _, repo := range trigger.Repositories {
			valid := repositoryName.MatchString(repo)
			if trigger.Scope == "organization" {
				valid = ownerName.MatchString(repo)
			}
			if !valid {
				return fmt.Errorf("invalid repository or organization")
			}
		}
		for _, branch := range trigger.Branches {
			if strings.TrimSpace(branch) == "" || strings.ContainsAny(branch, "*?[]\n\r") {
				return fmt.Errorf("branch filters must be exact names")
			}
		}
		if (trigger.Type == "pr" || trigger.Type == "ci") && len(trigger.Events) == 0 {
			return fmt.Errorf("PR and CI triggers require explicit actions or outcomes")
		}
		for _, event := range trigger.Events {
			if !safeID.MatchString(event) {
				return fmt.Errorf("invalid event filter")
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown trigger type")
	}
}

func Match(trigger verk.Trigger, event Event) bool {
	if Validate(trigger) != nil || trigger.Type != event.Type {
		return false
	}
	if trigger.Type == "webhook" {
		return trigger.SourceRef == event.Source
	}
	if event.Source != "github" || !repositoryName.MatchString(event.Repository) {
		return false
	}
	authorized := false
	for _, scope := range trigger.Repositories {
		candidate := event.Repository
		if trigger.Scope == "organization" {
			candidate = strings.Split(candidate, "/")[0]
		}
		if strings.EqualFold(candidate, scope) {
			authorized = true
			break
		}
	}
	if !authorized {
		return false
	}
	if len(trigger.Branches) > 0 && !includes(trigger.Branches, event.Branch) {
		return false
	}
	return len(trigger.Events) == 0 || includes(trigger.Events, event.Action)
}
func includes(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
func Key(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "verk-event:" + hex.EncodeToString(sum[:])
}
