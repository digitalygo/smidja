package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/clientextensions"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type extensionActivation struct {
	ID       string `json:"id"`
	ClientID string `json:"client_id"`
	Revision uint64 `json:"revision"`
	Status   string `json:"status"`
}

func (s *Server) startClientExtensionSync() {
	if s.cfg.Private == nil {
		return
	}
	s.launch("private:client-sync", func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			s.syncClientExtensions(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (s *Server) syncClientExtensions(ctx context.Context) {
	if s.cfg.Private == nil {
		return
	}
	entities := s.cfg.Private.List("extension", true)
	for _, client := range s.cfg.Clients.List() {
		if !client.Online || !client.Inventory.ExtensionControls {
			continue
		}
		controls := map[string]machineclient.ExtensionControl{}
		for _, entity := range entities {
			var definition privateconfig.Extension
			if json.Unmarshal(entity.Payload, &definition) != nil || definition.ClientID != client.ID {
				continue
			}
			if entity.ArchivedAt == nil {
				controls[entity.ID] = machineclient.ExtensionControl{ID: entity.ID, Revision: entity.Revision, Definition: definition}
			}
		}
		for _, advertised := range client.Inventory.ToolDefinitions {
			if advertised.PrivateRevision == 0 {
				continue
			}
			if control, ok := controls[advertised.ID]; ok {
				if control.Revision == advertised.PrivateRevision {
					delete(controls, advertised.ID)
				}
				continue
			}
			revision := advertised.PrivateRevision + 1
			if entity, err := s.cfg.Private.Get(advertised.ID); err == nil && entity.Revision > advertised.PrivateRevision {
				revision = entity.Revision
			}
			controls[advertised.ID] = machineclient.ExtensionControl{ID: advertised.ID, Revision: revision, Archived: true, Definition: privateconfig.Extension{ClientID: client.ID}}
		}
		for _, control := range controls {
			if ctx.Err() != nil {
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			s.cfg.Clients.ApplyExtension(callCtx, client.ID, control)
			cancel()
		}
	}
}

func (s *Server) clientExtensionActivations() []extensionActivation {
	result := []extensionActivation{}
	if s.cfg.Private == nil {
		return result
	}
	for _, entity := range s.cfg.Private.List("extension", true) {
		var definition privateconfig.Extension
		if json.Unmarshal(entity.Payload, &definition) != nil || definition.ClientID == "" || definition.ClientID == "manager" {
			continue
		}
		item := extensionActivation{ID: entity.ID, ClientID: definition.ClientID, Revision: entity.Revision, Status: "pending: client offline or unknown"}
		for _, client := range s.cfg.Clients.List() {
			if client.ID != definition.ClientID || !client.Online {
				continue
			}
			item.Status = "pending: awaiting client activation"
			if !client.Inventory.ExtensionControls {
				item.Status = "pending: client requires state_dir and extension control support"
			}
			found := false
			for _, tool := range client.Inventory.ToolDefinitions {
				if tool.ID != entity.ID {
					continue
				}
				found = true
				if entity.ArchivedAt == nil && tool.PrivateRevision == entity.Revision {
					item.Status = "active"
				}
			}
			if entity.ArchivedAt != nil && !found {
				item.Status = "archived"
			}
		}
		result = append(result, item)
	}
	return result
}

func (s *Server) executeClientTool(ctx context.Context, clientID, name string, args json.RawMessage) machinetools.Result {
	if strings.HasPrefix(name, "private:") {
		if s.cfg.Private == nil {
			return toolResult(nil, errors.New("private extension catalog unavailable"))
		}
		separator := strings.LastIndex(name, "@")
		if separator < 0 {
			return toolResult(nil, errors.New("private extension revision required"))
		}
		entity, err := s.cfg.Private.Get(name[:separator])
		if err != nil {
			return toolResult(nil, err)
		}
		var definition privateconfig.Extension
		if entity.Kind != "extension" || entity.ArchivedAt != nil || json.Unmarshal(entity.Payload, &definition) != nil || definition.ClientID != clientID || clientextensions.Route(entity.ID, entity.Revision) != name {
			return toolResult(nil, errors.New("private extension is not approved for this client and revision"))
		}
	}
	return s.cfg.Clients.Execute(ctx, clientID, name, args)
}

func (s *Server) approvedClientDefinition(clientID string, tool machineclient.ToolDefinition) bool {
	if s.cfg.Private == nil {
		return false
	}
	entity, err := s.cfg.Private.Get(tool.ID)
	if err != nil || entity.Kind != "extension" || entity.ArchivedAt != nil || entity.Revision != tool.PrivateRevision || clientextensions.Route(entity.ID, entity.Revision) != tool.Route {
		return false
	}
	var definition privateconfig.Extension
	return json.Unmarshal(entity.Payload, &definition) == nil && definition.ClientID == clientID
}
