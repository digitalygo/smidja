package verkrules

const MaxDepth = 32
const MaxNodes = 512

type Operand struct {
	Mode  string `json:"mode"`
	Type  string `json:"type,omitempty"`
	Value any    `json:"value"`
}

type Rule struct {
	Kind     string   `json:"kind"`
	Op       string   `json:"op,omitempty"`
	Children []*Rule  `json:"children,omitempty"`
	Operator string   `json:"operator,omitempty"`
	Left     *Operand `json:"left,omitempty"`
	Right    *Operand `json:"right,omitempty"`
}

type Resolution struct {
	Found  bool   `json:"found"`
	Value  any    `json:"value"`
	Type   string `json:"type,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Resolver func(string) (Resolution, error)

type Result struct {
	Status string   `json:"status"`
	Value  *bool    `json:"value"`
	Errors []string `json:"errors"`
}

type Branch struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Rule  *Rule  `json:"rule"`
}

type BranchTrace struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Result
}

type RouteResult struct {
	Status   string        `json:"status"`
	BranchID *string       `json:"branchId"`
	Trace    []BranchTrace `json:"trace"`
	Errors   []string      `json:"errors,omitempty"`
}

func invalid(errors []string) Result {
	return Result{Status: "invalid", Errors: errors}
}

func result(value bool) Result {
	status := "false"
	if value {
		status = "true"
	}
	return Result{Status: status, Value: &value, Errors: []string{}}
}
