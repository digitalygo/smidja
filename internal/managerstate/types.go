package managerstate

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("record not found")
var ErrConflict = errors.New("state conflict")
var ErrClosed = errors.New("store closed")

const (
	Queued    = "queued"
	Running   = "running"
	Blocked   = "blocked"
	Paused    = "paused"
	Completed = "completed"
	Cancelled = "cancelled"
)

type Decision string

const (
	DecisionBlocked    Decision = "BLOCKED"
	DecisionNotBlocked Decision = "NOT BLOCKED"
)

type Phase struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	TeamRef string `json:"team_ref"`
}

type Workflow struct {
	InputSchema     json.RawMessage            `json:"input_schema,omitempty"`
	AllowedInvokers []string                   `json:"allowed_invokers"`
	ToolDefinitions map[string]json.RawMessage `json:"tool_definitions,omitempty"`
	ClientID        string                     `json:"client_id,omitempty"`
	TeamDefinitions map[string]json.RawMessage `json:"team_definitions,omitempty"`
	ID              string                     `json:"id"`
	Version         string                     `json:"version"`
	Phases          []Phase                    `json:"phases"`
}

type StartTask struct {
	Workflow        Workflow        `json:"workflow"`
	Inputs          json.RawMessage `json:"inputs"`
	Focus           string          `json:"focus,omitempty"`
	OriginChatID    string          `json:"origin_chat_id,omitempty"`
	OriginTurnID    string          `json:"origin_turn_id,omitempty"`
	OriginSessionID string          `json:"origin_session_id,omitempty"`
	InvocationKey   string          `json:"invocation_key,omitempty"`
}

type Attempt struct {
	Number          int       `json:"number"`
	RequestID       string    `json:"request_id"`
	ChatID          string    `json:"chat_id,omitempty"`
	TurnID          string    `json:"turn_id,omitempty"`
	Output          string    `json:"output"`
	OutputRecorded  bool      `json:"output_recorded"`
	Decision        Decision  `json:"decision,omitempty"`
	ClassifierModel string    `json:"classifier_model,omitempty"`
	StartedAt       time.Time `json:"started_at"`
}

type PhaseState struct {
	Phase    Phase     `json:"phase"`
	Status   string    `json:"status"`
	Attempts []Attempt `json:"attempts"`
}

type Task struct {
	CancelRequested bool            `json:"cancel_requested"`
	CancelError     string          `json:"cancel_error,omitempty"`
	ID              string          `json:"id"`
	Workflow        Workflow        `json:"workflow"`
	Inputs          json.RawMessage `json:"inputs"`
	Focus           string          `json:"focus,omitempty"`
	OriginChatID    string          `json:"origin_chat_id,omitempty"`
	OriginTurnID    string          `json:"origin_turn_id,omitempty"`
	OriginSessionID string          `json:"origin_session_id,omitempty"`
	InvocationKey   string          `json:"invocation_key,omitempty"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	PhaseIndex      int             `json:"phase_index"`
	Phases          []PhaseState    `json:"phases"`
	Clarifications  []string        `json:"clarifications"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type PhaseOutput struct {
	PhaseID  string   `json:"phase_id"`
	Attempt  int      `json:"attempt"`
	Output   string   `json:"output"`
	Decision Decision `json:"decision,omitempty"`
}

type PhaseContext struct {
	Inputs         json.RawMessage `json:"inputs"`
	Focus          string          `json:"focus,omitempty"`
	Clarifications []string        `json:"clarifications"`
	Outputs        []PhaseOutput   `json:"outputs"`
}

type Message struct {
	ID      string    `json:"id"`
	Role    string    `json:"role"`
	Content string    `json:"content"`
	ChatID  string    `json:"chat_id,omitempty"`
	TurnID  string    `json:"turn_id,omitempty"`
	At      time.Time `json:"at"`
}

type SessionTurn struct {
	Output         string `json:"output,omitempty"`
	OutputRecorded bool   `json:"output_recorded"`
	RequestID      string `json:"request_id"`
	TurnID         string `json:"turn_id,omitempty"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	LastSequence   uint64 `json:"last_sequence"`
}

type Session struct {
	TeamRef        string       `json:"team_ref,omitempty"`
	TeamID         string       `json:"team_id,omitempty"`
	WorkflowTaskID string       `json:"workflow_task_id,omitempty"`
	LastSequence   uint64       `json:"last_sequence"`
	Turn           *SessionTurn `json:"turn,omitempty"`
	ID             string       `json:"id"`
	Title          string       `json:"title"`
	ChatID         string       `json:"chat_id,omitempty"`
	Messages       []Message    `json:"messages"`
	CreatedAt      time.Time    `json:"created_at"`
}

type ToolOperation struct {
	CallID   string          `json:"call_id"`
	ChatID   string          `json:"chat_id"`
	TurnID   string          `json:"turn_id"`
	ToolName string          `json:"tool_name"`
	Args     json.RawMessage `json:"args"`
	ClientID string          `json:"client_id"`
	Status   string          `json:"status"`
	Output   string          `json:"output"`
	IsError  bool            `json:"is_error"`
}

type event struct {
	TeamRef        string         `json:"team_ref,omitempty"`
	TeamID         string         `json:"team_id,omitempty"`
	Tool           *ToolOperation `json:"tool,omitempty"`
	RemoteSequence uint64         `json:"remote_sequence,omitempty"`
	SchemaVersion  int            `json:"schema_version"`
	ID             string         `json:"event_id"`
	Seq            uint64         `json:"seq"`
	At             time.Time      `json:"at"`
	Type           string         `json:"type"`
	TaskID         string         `json:"task_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Start          *StartTask     `json:"start,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	ChatID         string         `json:"chat_id,omitempty"`
	TurnID         string         `json:"turn_id,omitempty"`
	Text           string         `json:"text,omitempty"`
	Decision       Decision       `json:"decision,omitempty"`
	Model          string         `json:"model,omitempty"`
	Message        *Message       `json:"message,omitempty"`
}
