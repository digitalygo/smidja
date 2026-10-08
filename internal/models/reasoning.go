package models

import (
	"encoding/json"
	"strings"
)

var GatewayReasoningEfforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

type ReasoningInfo struct {
	Known bool

	Supported bool

	Mandatory bool

	EffortSelection bool

	Efforts string

	Allowlist bool
}

func (r ReasoningInfo) AllowsEffort(effort string) bool {
	if !r.Known || !r.Supported || !r.EffortSelection {
		return false
	}
	if !r.Allowlist {
		return true
	}
	for _, allowed := range strings.Split(r.Efforts, ",") {
		if allowed == effort {
			return true
		}
	}
	return false
}

func parseReasoningMetadata(raw json.RawMessage, supportedParameters []string) ReasoningInfo {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return reasoningFromParameters(supportedParameters)
	case trimmed == "true":
		return ReasoningInfo{Known: true, Supported: true}
	case trimmed == "false":
		return ReasoningInfo{Known: true}
	}
	if !strings.HasPrefix(trimmed, "{") {
		return ReasoningInfo{}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ReasoningInfo{}
	}
	if object == nil {
		return ReasoningInfo{}
	}
	info := ReasoningInfo{Known: true, Supported: true}
	if mandatory, ok := parseReasoningBool(object["mandatory"]); ok {
		info.Mandatory = mandatory
	}
	efforts, allowlist, present := reasoningEfforts(object)
	if !present {
		return info
	}
	info.EffortSelection = true
	info.Allowlist = allowlist
	info.Efforts = efforts
	return info
}

func reasoningFromParameters(supportedParameters []string) ReasoningInfo {
	for _, parameter := range supportedParameters {
		if strings.TrimSpace(parameter) == "reasoning" {
			return ReasoningInfo{Known: true, Supported: true}
		}
	}
	return ReasoningInfo{}
}

func reasoningEfforts(object map[string]json.RawMessage) (string, bool, bool) {
	for _, key := range []string{"supported_efforts", "efforts", "values"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "null" {
			return "", false, true
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return "", false, false
		}
		return strings.Join(normalizeEfforts(list), ","), true, true
	}
	return "", false, false
}

func normalizeEfforts(efforts []string) []string {
	out := make([]string, 0, len(efforts))
	for _, effort := range efforts {
		trimmed := strings.ToLower(strings.TrimSpace(effort))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func parseReasoningBool(raw json.RawMessage) (bool, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}
