package machineclient

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser origins are not accepted by the client protocol", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/api/clients/connect":
		b.connect(w, r)
	case "/api/clients/heartbeat":
		b.heartbeat(w, r)
	case "/api/clients/poll":
		b.poll(w, r)
	case "/api/clients/result":
		b.result(w, r)
	default:
		http.NotFound(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	reader := http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(out); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(w, "multiple JSON values are not allowed", http.StatusBadRequest)
		return false
	}
	return true
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func bearer(r *http.Request) string {
	prefix := "Bearer "
	if !strings.HasPrefix(r.Header.Get("Authorization"), prefix) {
		return ""
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), prefix)
}

func (b *Broker) connect(w http.ResponseWriter, r *http.Request) {
	var registration Registration
	if !decode(w, r, &registration) {
		return
	}
	if registration.ProtocolVersion != ProtocolVersion {
		http.Error(w, "unsupported client protocol", http.StatusConflict)
		return
	}
	if !validInventory(registration.Inventory) || len(registration.Name) > 256 {
		http.Error(w, "invalid inventory", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.authenticate(registration.ClientID, bearer(r))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	previous := c.status
	c.status.Inventory = copyInventory(registration.Inventory)
	c.status.LastSeen = time.Now()
	if registration.Name != "" {
		c.status.Name = registration.Name
	}
	b.inventory[c.status.ID] = c.status
	if err := b.saveInventory(); err != nil {
		c.status = previous
		b.inventory[c.status.ID] = previous
		http.Error(w, "cannot persist client inventory", http.StatusInternalServerError)
		return
	}
	c.connected = true
	sendJSON(w, http.StatusOK, map[string]any{"protocol_version": ProtocolVersion})
}

func (b *Broker) heartbeat(w http.ResponseWriter, r *http.Request) {
	var heartbeat Heartbeat
	if !decode(w, r, &heartbeat) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.authenticate(heartbeat.ClientID, bearer(r))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !c.connected {
		http.Error(w, "register inventory before heartbeat", http.StatusConflict)
		return
	}
	c.status.LastSeen = time.Now()
	reply := Heartbeat{ClientID: heartbeat.ClientID}
	for id := range c.cancelled {
		reply.Cancelled = append(reply.Cancelled, id)
	}
	for _, id := range heartbeat.Cancelled {
		delete(c.cancelled, id)
	}
	sendJSON(w, http.StatusOK, reply)
}

func (b *Broker) poll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clientID := r.URL.Query().Get("client_id")
	b.mu.Lock()
	c, ok := b.authenticate(clientID, bearer(r))
	if !ok {
		b.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !c.connected {
		b.mu.Unlock()
		http.Error(w, "register inventory before polling", http.StatusConflict)
		return
	}
	c.status.LastSeen = time.Now()
	b.mu.Unlock()
	timer := time.NewTimer(b.cfg.PollTimeout)
	defer timer.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.revoked:
			http.Error(w, "client revoked", http.StatusUnauthorized)
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case id := <-c.queue:
			b.mu.Lock()
			p, exists := b.pending[id]
			current := b.clients[clientID]
			if !exists || current != c || p.delivered || p.execution.ClientID != clientID {
				b.mu.Unlock()
				continue
			}
			if !time.Now().Before(p.execution.Deadline) {
				b.mu.Unlock()
				continue
			}
			p.delivered = true
			execution := p.execution
			b.mu.Unlock()
			sendJSON(w, http.StatusOK, execution)
			return
		}
	}
}

func (b *Broker) result(w http.ResponseWriter, r *http.Request) {
	var envelope ResultEnvelope
	if !decode(w, r, &envelope) {
		return
	}
	canonical, err := json.Marshal(envelope.Result)
	if err != nil {
		http.Error(w, "invalid result", http.StatusBadRequest)
		return
	}
	digest := sha256.Sum256(canonical)
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.authenticate(envelope.ClientID, bearer(r))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c.status.LastSeen = time.Now()
	if done, ok := b.completed[envelope.RequestID]; ok {
		if done.clientID != envelope.ClientID || done.digest != digest {
			http.Error(w, "result conflicts with receipt", http.StatusConflict)
			return
		}
		sendJSON(w, http.StatusOK, map[string]bool{"accepted": true})
		return
	}
	p, ok := b.pending[envelope.RequestID]
	if !ok {
		http.Error(w, "operation is no longer awaiting a result; do not repeat execution", http.StatusGone)
		return
	}
	if p.execution.ClientID != envelope.ClientID || !p.delivered {
		http.Error(w, "operation does not belong to this dispatch", http.StatusConflict)
		return
	}
	delete(b.pending, envelope.RequestID)
	b.recordReceipt(envelope.RequestID, envelope.ClientID, digest)
	p.result <- envelope.Result
	sendJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

func validInventory(in Inventory) bool {
	if in.OS == "" || in.Arch == "" || len(in.Tools) > 1024 || len(in.Teams) > 1024 || len(in.Packages) > 1024 || len(in.ToolDefinitions) > 1024 || len(in.TeamDefinitions) > 1024 || len(in.WorkflowDefinitions) > 1024 || len(in.SkillDefinitions) > 1024 {
		return false
	}
	routes := map[string]bool{}
	for _, route := range in.Tools {
		if route == "" || routes[route] || strings.HasPrefix(route, "__smidja_") {
			return false
		}
		routes[route] = true
	}
	revisions := map[PackageRevision]bool{}
	for _, revision := range in.Packages {
		if revision.ID == "" || revision.Version == "" || revision.Digest == "" || revisions[revision] {
			return false
		}
		revisions[revision] = true
	}
	ids := map[string]bool{}
	for _, definition := range in.ToolDefinitions {
		validProvenance := revisions[definition.Package] && definition.PrivateRevision == 0
		if definition.PrivateRevision > 0 {
			validProvenance = definition.Package == (PackageRevision{}) && privateExtensionID.MatchString(definition.ID) && definition.Route == definition.ID+"@"+strconv.FormatUint(definition.PrivateRevision, 10)
		}
		if definition.ID == "" || ids[definition.ID] || !routes[definition.Route] || !validProvenance || !json.Valid(definition.Parameters) {
			return false
		}
		ids[definition.ID] = true
	}
	for _, definitions := range [][]CatalogDefinition{in.TeamDefinitions, in.WorkflowDefinitions} {
		for _, definition := range definitions {
			if definition.ID == "" || ids[definition.ID] || !revisions[definition.Package] || !json.Valid(definition.Definition) {
				return false
			}
			ids[definition.ID] = true
		}
	}
	for _, definition := range in.SkillDefinitions {
		if definition.ID == "" || ids[definition.ID] || !revisions[definition.Package] {
			return false
		}
		ids[definition.ID] = true
	}

	return true
}
