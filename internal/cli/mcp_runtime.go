package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/mcp"
)

type mcpRuntime struct {
	mu      sync.Mutex
	clients []*mcp.Client
}

func (r *mcpRuntime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	clients := r.clients
	r.clients = nil
	r.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}

func (r *mcpRuntime) add(c *mcp.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients = append(r.clients, c)
}

func loadMCPConfig(home, cwd string) (*mcp.FileConfig, map[string]bool, error) {
	userPath := filepath.Join(home, ".smidja", "mcp.json")
	wsPath := filepath.Join(cwd, ".smidja", "mcp.json")
	var userCfg, wsCfg *mcp.FileConfig
	if _, err := os.Stat(userPath); err == nil {
		userCfg, err = mcp.ReadConfig(userPath)
		if err != nil {
			return nil, nil, fmt.Errorf("smidja: mcp: %w", err)
		}
	}
	if _, err := os.Stat(wsPath); err == nil {
		wsCfg, err = mcp.ReadConfig(wsPath)
		if err != nil {
			return nil, nil, fmt.Errorf("smidja: mcp: %w", err)
		}
	}
	workspaceIDs := map[string]bool{}
	if wsCfg != nil {
		for id, srv := range wsCfg.Servers {
			if srv.Enabled {
				workspaceIDs[id] = true
			}
		}
	}
	return mcp.MergeConfigs(userCfg, wsCfg), workspaceIDs, nil
}

func startMCP(ctx context.Context, cfg *mcp.FileConfig, workspaceIDs map[string]bool, allowWorkspace bool, catalog *extensions.ToolCatalog, resolveEnv func(string) (string, bool), stderr io.Writer) (*mcpRuntime, error) {
	rt := &mcpRuntime{}
	if cfg == nil {
		return rt, nil
	}
	ids := make([]string, 0, len(cfg.Servers))
	for id := range cfg.Servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		srv := cfg.Servers[id]
		if !srv.Enabled {
			continue
		}
		if workspaceIDs[id] && !allowWorkspace {
			fmt.Fprintf(stderr, "smidja: mcp: skipping workspace-defined server %q, pass --allow-workspace-mcp to enable it\n", id)
			continue
		}
		client, err := mcp.Spawn(ctx, mcp.SpawnConfig{Cfg: srv, ResolveEnv: resolveEnv})
		if err != nil {
			rt.Close()
			if srv.Required {
				return nil, fmt.Errorf("smidja: mcp: server %q: %w", id, err)
			}
			fmt.Fprintf(stderr, "smidja: mcp: server %q unavailable, skipping: %v\n", id, err)
			continue
		}
		tools, err := mcp.ToAgentTools(ctx, client, id)
		if err != nil {
			client.Close()
			rt.Close()
			if srv.Required {
				return nil, fmt.Errorf("smidja: mcp: server %q: %w", id, err)
			}
			fmt.Fprintf(stderr, "smidja: mcp: server %q tool discovery failed, skipping: %v\n", id, err)
			continue
		}
		for _, t := range tools {
			if rerr := catalog.RegisterSource(t, "mcp:"+id); rerr != nil {
				client.Close()
				rt.Close()
				return nil, fmt.Errorf("smidja: mcp: register tool from %q: %w", id, rerr)
			}
		}
		rt.add(client)
	}
	return rt, nil
}
