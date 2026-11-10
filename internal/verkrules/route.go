package verkrules

import "strings"

func Route(branches []Branch, defaultID string, resolve Resolver) RouteResult {
	trace := []BranchTrace{}
	fail := func(errors []string) RouteResult { return RouteResult{Status: "invalid", Trace: trace, Errors: errors} }
	if branches == nil {
		return fail([]string{"branches must be an array"})
	}
	ids := map[string]bool{}
	for _, branch := range branches {
		if strings.TrimSpace(branch.ID) == "" || ids[branch.ID] {
			return fail([]string{"branches require unique nonempty string ids"})
		}
		ids[branch.ID] = true
	}
	for _, branch := range branches {
		evaluated := Evaluate(branch.Rule, resolve)
		label := branch.Label
		if label == "" {
			label = branch.ID
		}
		trace = append(trace, BranchTrace{ID: branch.ID, Label: label, Result: evaluated})
		if evaluated.Status == "invalid" {
			return fail(evaluated.Errors)
		}
		if *evaluated.Value {
			id := branch.ID
			return RouteResult{Status: "matched", BranchID: &id, Trace: trace}
		}
	}
	if strings.TrimSpace(defaultID) == "" {
		return fail([]string{"No branch matched and default route is missing"})
	}
	return RouteResult{Status: "default", BranchID: &defaultID, Trace: trace}
}
