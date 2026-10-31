package machineclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type registeredClient struct {
	status    ClientStatus
	connected bool
	tokenHash [32]byte
	queue     chan string
	revoked   chan struct{}
	cancelled map[string]struct{}
}

type pendingExecution struct {
	execution Execution
	result    chan machinetools.Result
	delivered bool
}

type receipt struct {
	clientID string
	digest   [32]byte
}

type Broker struct {
	closed          bool
	credentials     *credentialJournal
	mu              sync.Mutex
	cfg             BrokerConfig
	clients         map[string]*registeredClient
	pending         map[string]*pendingExecution
	completed       map[string]receipt
	completionOrder []string
	inventory       map[string]ClientStatus
	initErr         error
}

func NewBroker(cfg BrokerConfig) *Broker {
	if cfg.HeartbeatTTL <= 0 {
		cfg.HeartbeatTTL = 45 * time.Second
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 20 * time.Second
	}
	if cfg.ExecutionTimeout <= 0 {
		cfg.ExecutionTimeout = 120 * time.Second
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = 64
	}
	if cfg.CompletedLimit <= 0 {
		cfg.CompletedLimit = 1024
	}
	b := &Broker{cfg: cfg, clients: make(map[string]*registeredClient), pending: make(map[string]*pendingExecution), completed: make(map[string]receipt), inventory: make(map[string]ClientStatus)}
	b.initErr = b.loadInventory()
	if b.initErr == nil {
		b.initErr = b.loadCredentials()
	}
	return b
}

func (b *Broker) Handler() http.Handler { return b }

func (b *Broker) AddClient(id, name, token string) error {
	if id == "manager" {
		return errors.New("client ID manager is reserved for local execution")
	}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" || len(id) > 128 || len(name) > 256 || len(token) < 16 {
		return errors.New("client ID, name and a token of at least 16 bytes are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.initErr != nil {
		return b.initErr
	}
	if b.credentials != nil {
		if _, recorded := b.credentials.records[id]; recorded {
			return nil
		}
	}
	if _, exists := b.clients[id]; exists {
		return errors.New("client already registered; revoke it before replacing its credentials")
	}
	hash := sha256.Sum256([]byte(token))
	for _, existing := range b.clients {
		if subtle.ConstantTimeCompare(hash[:], existing.tokenHash[:]) == 1 {
			return errors.New("each client requires a unique token")
		}
	}
	b.addClientHash(id, name, hash)
	return nil
}

func (b *Broker) Revoke(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.initErr != nil {
		return b.initErr
	}
	if b.credentials != nil {
		record, exists := b.credentials.records[id]
		if c, ok := b.clients[id]; ok {
			record.ID = id
			record.Name = c.status.Name
			record.Hash = hex.EncodeToString(c.tokenHash[:])
			exists = true
		}
		if !exists {
			return errors.New("client not found")
		}
		if !record.Revoked {
			record.Revoked = true
			if err := b.credentials.append(record); err != nil {
				b.initErr = err
				return errors.New("revocation outcome uncertain; inspect the journal")
			}
		}
	}
	b.revokeMemory(id)
	return b.saveInventory()
}
func (b *Broker) revokeMemory(id string) {
	if c, ok := b.clients[id]; ok {
		close(c.revoked)
		delete(b.clients, id)
	}
	delete(b.inventory, id)
	for requestID, p := range b.pending {
		if p.execution.ClientID == id {
			p.result <- failure("client_revoked", "Client access was revoked; effects already executed may require verification")
			delete(b.pending, requestID)
		}
	}
}

func (b *Broker) List() []ClientStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	statuses := make([]ClientStatus, 0, len(b.clients)+len(b.inventory))
	for id, saved := range b.inventory {
		if _, ok := b.clients[id]; !ok {
			saved.Online = false
			saved.Inventory = copyInventory(saved.Inventory)
			statuses = append(statuses, saved)
		}
	}
	for _, c := range b.clients {
		status := c.status
		status.Online = b.online(c)
		status.Inventory = copyInventory(status.Inventory)
		statuses = append(statuses, status)
	}
	if b.credentials != nil {
		for _, record := range b.credentials.records {
			if record.Revoked {
				statuses = append(statuses, ClientStatus{ID: record.ID, Name: record.Name, Revoked: true})
			}
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	return statuses
}

func (b *Broker) online(c *registeredClient) bool {
	return c.connected && !c.status.LastSeen.IsZero() && time.Since(c.status.LastSeen) < b.cfg.HeartbeatTTL
}

func copyInventory(in Inventory) Inventory {
	data, _ := json.Marshal(in)
	var result Inventory
	_ = json.Unmarshal(data, &result)
	return result
}

func failure(code, message string) machinetools.Result {
	return machinetools.Result{OK: false, Code: code, Error: message}
}

func (b *Broker) Execute(ctx context.Context, clientID, tool string, args json.RawMessage) machinetools.Result {
	if strings.HasPrefix(tool, "__smidja_") {
		return failure("tool_unavailable", "Reserved client control commands are not public tools")
	}
	return b.dispatch(ctx, clientID, tool, args, false)
}
func (b *Broker) ApplyExtension(ctx context.Context, clientID string, definition ExtensionControl) machinetools.Result {
	if definition.Definition.ClientID != clientID || !privateExtensionID.MatchString(definition.ID) || definition.Revision == 0 {
		return failure("invalid_arguments", "Private extension control binding is invalid")
	}
	args, err := json.Marshal(definition)
	if err != nil {
		return failure("invalid_arguments", "Invalid extension control payload")
	}
	return b.dispatch(ctx, clientID, ApplyExtensionControl, args, true)
}
func (b *Broker) dispatch(ctx context.Context, clientID, tool string, args json.RawMessage, control bool) machinetools.Result {
	if !json.Valid(args) {
		return failure("invalid_arguments", "Tool arguments must be valid JSON")
	}
	if err := ctx.Err(); err != nil {
		return failure("cancelled", err.Error())
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return failure("internal_error", "Cannot allocate request ID")
	}
	requestID := hex.EncodeToString(nonce[:])
	timeout := b.cfg.ExecutionTimeout
	b.mu.Lock()
	if b.initErr != nil {
		b.mu.Unlock()
		return failure("broker_unavailable", "Client broker is closed or its durable state is unavailable")
	}
	c, ok := b.clients[clientID]
	if !ok || !b.online(c) {
		b.mu.Unlock()
		return failure("client_offline", "The selected client is offline")
	}
	if !control {
		if seconds, ok := c.status.Inventory.PackageTimeouts[tool]; ok {
			var err error
			timeout, err = privateconfig.ExtensionTimeout(seconds)
			if err != nil {
				b.mu.Unlock()
				return failure("invalid_definition", err.Error())
			}
		}
		for _, definition := range c.status.Inventory.ToolDefinitions {
			if definition.Route == tool && (definition.PrivateRevision > 0 || definition.Package.ID != "") {
				var err error
				timeout, err = privateconfig.ExtensionTimeout(definition.TimeoutSeconds)
				if err != nil {
					b.mu.Unlock()
					return failure("invalid_definition", err.Error())
				}
				break
			}
		}
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	allowed := control
	for _, name := range c.status.Inventory.Tools {
		if name == tool {
			allowed = true
			break
		}
	}
	if !allowed {
		b.mu.Unlock()
		return failure("tool_unavailable", "The selected client has not advertised this tool")
	}
	if len(b.pending) >= b.cfg.MaxPending {
		b.mu.Unlock()
		return failure("busy", "Remote operation queue is full")
	}
	p := &pendingExecution{execution: Execution{Control: control, ProtocolVersion: ProtocolVersion, RequestID: requestID, ClientID: clientID, Tool: tool, Args: append(json.RawMessage(nil), args...), Deadline: deadline}, result: make(chan machinetools.Result, 1)}
	b.pending[requestID] = p
	select {
	case c.queue <- requestID:
	default:
		delete(b.pending, requestID)
		b.mu.Unlock()
		return failure("busy", "Client operation queue is full")
	}
	b.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case result := <-p.result:
		return result
	case <-ctx.Done():
		return b.abandon(requestID, ctx.Err().Error())
	case <-timer.C:
		return b.abandon(requestID, "Remote operation deadline exceeded")
	}
}

func (b *Broker) abandon(id, reason string) machinetools.Result {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[id]
	if !ok {
		return failure("outcome_unknown", reason+"; verify the result before retrying")
	}
	delete(b.pending, id)
	if c := b.clients[p.execution.ClientID]; c != nil && p.delivered {
		c.cancelled[id] = struct{}{}
	}
	if p.delivered {
		return failure("outcome_unknown", reason+"; execution was dispatched and must not be repeated blindly")
	}
	return failure("cancelled", reason+"; execution was not dispatched")
}

func (b *Broker) authenticate(id, token string) (*registeredClient, bool) {
	if b.initErr != nil {
		return nil, false
	}
	c, ok := b.clients[id]
	hash := sha256.Sum256([]byte(token))
	if !ok {
		var dummy [32]byte
		subtle.ConstantTimeCompare(hash[:], dummy[:])
		return nil, false
	}
	return c, subtle.ConstantTimeCompare(hash[:], c.tokenHash[:]) == 1
}

func (b *Broker) recordReceipt(id, clientID string, digest [32]byte) {
	b.completed[id] = receipt{clientID: clientID, digest: digest}
	b.completionOrder = append(b.completionOrder, id)
	if len(b.completionOrder) > b.cfg.CompletedLimit {
		old := b.completionOrder[0]
		b.completionOrder = b.completionOrder[1:]
		delete(b.completed, old)
	}
}
