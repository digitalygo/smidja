package machineclient

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
)

const ProtocolVersion = 1

type PackageRevision struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type ToolDefinition struct {
	ID          string          `json:"id"`
	Route       string          `json:"route"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Package     PackageRevision `json:"package"`
}
type CatalogDefinition struct {
	ID         string          `json:"id"`
	Package    PackageRevision `json:"package"`
	Definition json.RawMessage `json:"definition"`
}

type SkillDefinition struct {
	ID      string          `json:"id"`
	Package PackageRevision `json:"package"`
	Content string          `json:"content"`
}

type Inventory struct {
	PackageErrors       map[string]string   `json:"package_errors,omitempty"`
	SkillDefinitions    []SkillDefinition   `json:"skill_definitions,omitempty"`
	ToolDefinitions     []ToolDefinition    `json:"tool_definitions,omitempty"`
	TeamDefinitions     []CatalogDefinition `json:"team_definitions,omitempty"`
	WorkflowDefinitions []CatalogDefinition `json:"workflow_definitions,omitempty"`
	OS                  string              `json:"os"`
	Arch                string              `json:"arch"`
	Tools               []string            `json:"tools"`
	Teams               []string            `json:"teams,omitempty"`
	Packages            []PackageRevision   `json:"packages,omitempty"`
}

type ClientStatus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	Inventory Inventory `json:"inventory"`
}

type Registration struct {
	ProtocolVersion int       `json:"protocol_version"`
	ClientID        string    `json:"client_id"`
	Name            string    `json:"name"`
	Inventory       Inventory `json:"inventory"`
}

type Execution struct {
	ProtocolVersion int             `json:"protocol_version"`
	RequestID       string          `json:"request_id"`
	ClientID        string          `json:"client_id"`
	Tool            string          `json:"tool"`
	Args            json.RawMessage `json:"args"`
	Deadline        time.Time       `json:"deadline"`
}

type ResultEnvelope struct {
	ClientID  string              `json:"client_id"`
	RequestID string              `json:"request_id"`
	Result    machinetools.Result `json:"result"`
}

type Heartbeat struct {
	ClientID  string   `json:"client_id"`
	Cancelled []string `json:"cancelled,omitempty"`
}

type BrokerConfig struct {
	InventoryPath    string
	HeartbeatTTL     time.Duration
	PollTimeout      time.Duration
	ExecutionTimeout time.Duration
	MaxPending       int
	CompletedLimit   int
}

type Config struct {
	ManagerURL        string
	ClientID          string
	Name              string
	Token             string
	HTTPClient        *http.Client
	HeartbeatInterval time.Duration
	InventoryProvider func(context.Context) (Inventory, error)
	Inventory         Inventory
}

type Executor interface {
	Execute(context.Context, string, json.RawMessage) machinetools.Result
}
