package machineclient

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

const ProtocolVersion = 1
const ApplyExtensionControl = "__smidja_apply_extension"

var privateExtensionID = regexp.MustCompile(`^private:[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type ExtensionControl struct {
	ID         string                  `json:"id"`
	Revision   uint64                  `json:"revision"`
	Archived   bool                    `json:"archived"`
	Definition privateconfig.Extension `json:"definition"`
}
type ControlExecutor interface {
	ExecuteControl(context.Context, string, json.RawMessage) machinetools.Result
}

type PackageRevision struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type ToolDefinition struct {
	TimeoutSeconds  int             `json:"timeout_seconds,omitempty"`
	PrivateRevision uint64          `json:"private_revision,omitempty"`
	ID              string          `json:"id"`
	Route           string          `json:"route"`
	Description     string          `json:"description"`
	Parameters      json.RawMessage `json:"parameters"`
	Package         PackageRevision `json:"package"`
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
	PackageTimeouts     map[string]int      `json:"package_timeouts,omitempty"`
	ExtensionControls   bool                `json:"extension_controls,omitempty"`
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
	Revoked   bool      `json:"revoked,omitempty"`
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
	Control         bool            `json:"control,omitempty"`
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
	CredentialsPath  string
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
