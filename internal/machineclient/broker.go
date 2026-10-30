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
	if _, exists := b.clients[id]; exists {
		return errors.New("client already registered; revoke it before replacing its credentials")
	}
	hash := sha256.Sum256([]byte(token))
	for _, existing := range b.clients {
		if subtle.ConstantTimeCompare(hash[:], existing.tokenHash[:]) == 1 {
			return errors.New("each client requires a unique token")
		}
	}
	status := b.inventory[id]
	status.ID, status.Name, status.Online = id, name, false
	b.clients[id] = &registeredClient{status: status, tokenHash: hash, queue: make(chan string, b.cfg.MaxPending), revoked: make(chan struct{}), cancelled: make(map[string]struct{})}
	return nil
}

func (b *Broker) Revoke(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.clients[id]
	if !ok {
		delete(b.inventory, id)
		return b.saveInventory()
	}
	close(c.revoked)
	delete(b.clients, id)
	delete(b.inventory, id)
	for requestID, p := range b.pending {
		if p.execution.ClientID == id {
			p.result <- failure("client_revoked", "Client access was revoked; effects already executed may require verification")
			delete(b.pending, requestID)
		}
	}
	return b.saveInventory()
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
	deadline := time.Now().Add(b.cfg.ExecutionTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	b.mu.Lock()
	c, ok := b.clients[clientID]
	if !ok || !b.online(c) {
		b.mu.Unlock()
		return failure("client_offline", "The selected client is offline")
	}
	allowed := false
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
	p := &pendingExecution{execution: Execution{ProtocolVersion: ProtocolVersion, RequestID: requestID, ClientID: clientID, Tool: tool, Args: append(json.RawMessage(nil), args...), Deadline: deadline}, result: make(chan machinetools.Result, 1)}
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
