package verk

import (
	"encoding/json"
	"time"

	"github.com/digitalygo/smidja/internal/verkrules"
)

type Port struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type Value struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}
type Trigger struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Scope        string   `json:"scope,omitempty"`
	Repositories []string `json:"repositories,omitempty"`
	Branches     []string `json:"branches,omitempty"`
	Events       []string `json:"events,omitempty"`
	Schedule     string   `json:"schedule,omitempty"`
	Timezone     string   `json:"timezone,omitempty"`
	SourceRef    string   `json:"source_ref,omitempty"`
}
type ExitActions struct {
	SetState     bool   `json:"set_state"`
	State        string `json:"state"`
	ReturnData   bool   `json:"return_data"`
	NotifySmidr  bool   `json:"notify_smidr"`
	Intervention bool   `json:"intervention"`
}
type Node struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Title        string             `json:"title"`
	X            float64            `json:"x"`
	Y            float64            `json:"y"`
	Params       map[string]any     `json:"params,omitempty"`
	Outputs      []Port             `json:"outputs"`
	OutputSchema json.RawMessage    `json:"output_schema,omitempty"`
	Branches     []verkrules.Branch `json:"branches,omitempty"`
	DefaultID    string             `json:"default_id,omitempty"`
	Triggers     []Trigger          `json:"triggers,omitempty"`
	ExitActions  *ExitActions       `json:"exit_actions,omitempty"`
}
type Edge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	Port string `json:"port"`
	To   string `json:"to"`
}
type Graph struct {
	SchemaVersion int     `json:"schema_version"`
	Nodes         []Node  `json:"nodes"`
	Edges         []Edge  `json:"edges"`
	Variables     []Value `json:"variables,omitempty"`
	InputValues   []Value `json:"input_values,omitempty"`
}
type Run struct {
	ID             string          `json:"id"`
	NodeID         string          `json:"node_id"`
	Visit          int             `json:"visit"`
	Status         string          `json:"status"`
	RequestID      string          `json:"request_id"`
	Remote         json.RawMessage `json:"remote,omitempty"`
	Output         string          `json:"output"`
	OutputRecorded bool            `json:"output_recorded"`
	Fields         map[string]any  `json:"fields,omitempty"`
	Port           string          `json:"port,omitempty"`
	Trace          json.RawMessage `json:"trace,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
}
type Gate struct {
	ID             string     `json:"id"`
	RunID          string     `json:"run_id"`
	Kind           string     `json:"kind"`
	Prompt         string     `json:"prompt"`
	ProposalDigest string     `json:"proposal_digest,omitempty"`
	Deadline       *time.Time `json:"deadline,omitempty"`
	SourceRef      string     `json:"source_ref,omitempty"`
	Status         string     `json:"status"`
}
type Loop struct {
	Used   int `json:"used"`
	Budget int `json:"budget"`
}
type State struct {
	Revision       uint64          `json:"revision"`
	Status         string          `json:"status"`
	Cursor         string          `json:"cursor"`
	Runs           []Run           `json:"runs"`
	Inputs         map[string]any  `json:"inputs"`
	Variables      map[string]any  `json:"variables"`
	Loops          map[string]Loop `json:"loops"`
	Gate           *Gate           `json:"gate,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Clarifications []string        `json:"clarifications"`
	Result         map[string]any  `json:"result,omitempty"`
}
type Event struct {
	Type             string          `json:"type"`
	ExpectedRevision uint64          `json:"expected_revision"`
	RunID            string          `json:"run_id,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
	At               time.Time       `json:"at"`
	Remote           json.RawMessage `json:"remote,omitempty"`
	Output           string          `json:"output,omitempty"`
	Fields           map[string]any  `json:"fields,omitempty"`
	Port             string          `json:"port,omitempty"`
	Trace            json.RawMessage `json:"trace,omitempty"`
	Variables        map[string]any  `json:"variables,omitempty"`
	Gate             *Gate           `json:"gate,omitempty"`
	GateID           string          `json:"gate_id,omitempty"`
	ProposalDigest   string          `json:"proposal_digest,omitempty"`
	SourceRef        string          `json:"source_ref,omitempty"`
	Text             string          `json:"text,omitempty"`
	Budget           int             `json:"budget,omitempty"`
	TrustedOwner     bool            `json:"trusted_owner,omitempty"`
}
type Evaluation struct {
	Port        string          `json:"port,omitempty"`
	Fields      map[string]any  `json:"fields,omitempty"`
	Variables   map[string]any  `json:"variables,omitempty"`
	Trace       json.RawMessage `json:"trace,omitempty"`
	BlockReason string          `json:"block_reason,omitempty"`
	External    bool            `json:"external"`
}
type SchemaValidator func(json.RawMessage, json.RawMessage) error
