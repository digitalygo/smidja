package machineclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
)

type remoteClient struct {
	cfg       Config
	http      *http.Client
	base      *url.URL
	mu        sync.Mutex
	active    map[string]context.CancelFunc
	inventory Inventory
}

func Run(ctx context.Context, cfg Config, executor Executor) error {
	c, err := newRemoteClient(cfg)
	if err != nil {
		return err
	}
	if executor == nil {
		return errors.New("client executor is required")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err = c.refreshInventory(ctx, true); err != nil {
		return err
	}
	completed := make(map[string]ResultEnvelope)
	bindings := make(map[string][32]byte)
	var completionOrder []string
	heartbeatErrors := make(chan error, 1)
	go func() { heartbeatErrors <- c.beat(ctx); cancel() }()
	for {
		var execution Execution
		err = c.request(ctx, http.MethodGet, "/api/clients/poll?client_id="+url.QueryEscape(cfg.ClientID), nil, &execution)
		if err != nil {
			select {
			case heartbeatErr := <-heartbeatErrors:
				if heartbeatErr != nil {
					return heartbeatErr
				}
			default:
			}
			return err
		}
		if execution.RequestID == "" {
			continue
		}
		if execution.ProtocolVersion != ProtocolVersion || execution.ClientID != cfg.ClientID || execution.Deadline.IsZero() || execution.Tool == "" || !json.Valid(execution.Args) {
			return errors.New("manager returned an invalid execution binding")
		}
		allowed := false
		c.mu.Lock()
		for _, name := range c.inventory.Tools {
			if name == execution.Tool {
				allowed = true
				break
			}
		}
		c.mu.Unlock()
		if !allowed {
			return errors.New("manager requested an unadvertised tool")
		}
		if !time.Now().Before(execution.Deadline) {
			return errors.New("manager returned an expired execution")
		}
		bindingBytes, _ := json.Marshal(execution)
		binding := sha256.Sum256(bindingBytes)
		if prior, exists := completed[execution.RequestID]; exists {
			if bindings[execution.RequestID] != binding {
				return errors.New("repeated operation has a different binding")
			}
			if err := c.request(ctx, http.MethodPost, "/api/clients/result", prior, nil); err != nil {
				return err
			}
			continue
		}
		taskCtx, taskCancel := context.WithDeadline(ctx, execution.Deadline)
		c.mu.Lock()
		c.active[execution.RequestID] = taskCancel
		c.mu.Unlock()
		result := executor.Execute(taskCtx, execution.Tool, execution.Args)
		taskCancel()
		c.mu.Lock()
		delete(c.active, execution.RequestID)
		c.mu.Unlock()
		envelope := ResultEnvelope{ClientID: cfg.ClientID, RequestID: execution.RequestID, Result: result}
		completed[execution.RequestID] = envelope
		bindings[execution.RequestID] = binding
		completionOrder = append(completionOrder, execution.RequestID)
		if len(completionOrder) > 1024 {
			old := completionOrder[0]
			completionOrder = completionOrder[1:]
			delete(completed, old)
			delete(bindings, old)
		}
		if err = c.request(ctx, http.MethodPost, "/api/clients/result", envelope, nil); err != nil {
			return fmt.Errorf("result delivery failed; execution must not be repeated automatically: %w", err)
		}
	}
}

func newRemoteClient(cfg Config) (*remoteClient, error) {
	if cfg.ClientID == "manager" {
		return nil, errors.New("client ID manager is reserved for local execution")
	}
	parsed, err := url.Parse(cfg.ManagerURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("manager URL must be an absolute origin without credentials, path, query or fragment")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return nil, errors.New("manager connection requires HTTPS except on loopback")
	}
	if cfg.ClientID == "" || cfg.Name == "" || len(cfg.Token) < 16 {
		return nil, errors.New("client ID, name and token are required")
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	client := &http.Client{}
	if cfg.HTTPClient != nil {
		*client = *cfg.HTTPClient
	}
	if client.Timeout == 0 {
		client.Timeout = 45 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("client protocol redirects are forbidden")
	}
	return &remoteClient{cfg: cfg, http: client, base: parsed, active: make(map[string]context.CancelFunc)}, nil
}

func (c *remoteClient) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	endpoint := *c.base
	parts := strings.SplitN(path, "?", 2)
	endpoint.Path = parts[0]
	endpoint.RawQuery = ""
	if len(parts) == 2 {
		endpoint.RawQuery = parts[1]
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client transport failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("client protocol HTTP %d", response.StatusCode)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
		return err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 2<<20 {
		return errors.New("client protocol response too large")
	}
	if err = json.Unmarshal(data, output); err != nil {
		return errors.New("invalid client protocol JSON")
	}
	return nil
}

func (c *remoteClient) beat(ctx context.Context) error {
	timer := time.NewTicker(c.cfg.HeartbeatInterval)
	defer timer.Stop()
	var acknowledged []string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := c.refreshInventory(ctx, false); err != nil {
				return err
			}
			var reply Heartbeat
			if err := c.request(ctx, http.MethodPost, "/api/clients/heartbeat", Heartbeat{ClientID: c.cfg.ClientID, Cancelled: acknowledged}, &reply); err != nil {
				return err
			}
			if reply.ClientID != c.cfg.ClientID {
				return errors.New("heartbeat client binding mismatch")
			}
			c.mu.Lock()
			for _, id := range reply.Cancelled {
				if cancel := c.active[id]; cancel != nil {
					cancel()
				}
			}
			c.mu.Unlock()
			acknowledged = reply.Cancelled
		}
	}
}

func (c *remoteClient) refreshInventory(ctx context.Context, force bool) error {
	inventory := c.cfg.Inventory
	var err error
	if c.cfg.InventoryProvider != nil {
		inventory, err = c.cfg.InventoryProvider(ctx)
		if err != nil {
			return fmt.Errorf("refresh local inventory: %w", err)
		}
	}
	if inventory.OS == "" {
		inventory.OS = runtime.GOOS
	}
	if inventory.Arch == "" {
		inventory.Arch = runtime.GOARCH
	}
	if !validInventory(inventory) {
		return errors.New("invalid local inventory")
	}
	c.mu.Lock()
	previous, _ := json.Marshal(c.inventory)
	c.mu.Unlock()
	next, err := json.Marshal(inventory)
	if err != nil {
		return err
	}
	if len(next) > 1<<20 {
		return errors.New("local inventory exceeds 1 MiB")
	}
	if !force && bytes.Equal(previous, next) {
		return nil
	}
	registration := Registration{ProtocolVersion: ProtocolVersion, ClientID: c.cfg.ClientID, Name: c.cfg.Name, Inventory: inventory}
	c.mu.Lock()
	priorInventory := c.inventory
	c.inventory = copyInventory(inventory)
	c.mu.Unlock()
	if err = c.request(ctx, http.MethodPost, "/api/clients/connect", registration, nil); err != nil {
		c.mu.Lock()
		c.inventory = priorInventory
		c.mu.Unlock()
		return err
	}
	return nil
}
