package manager

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) deviceAPI(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Clients.PairingReady() {
		writeError(w, 503, errors.New("durable client pairing is unavailable"))
		return
	}
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/api/devices" {
		var input struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if decode(w, r, &input, 8192) != nil {
			return
		}
		pairing, err := s.cfg.Clients.PairClient(input.ID, input.Name, false)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		writeJSON(w, 201, pairing)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "revoke":
		if err := s.cfg.Clients.Revoke(parts[0]); err != nil {
			writeError(w, 409, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "revoked", "client_id": parts[0]})
	case "rotate":
		var input struct {
			Name string `json:"name"`
		}
		if decode(w, r, &input, 8192) != nil {
			return
		}
		pairing, err := s.cfg.Clients.PairClient(parts[0], input.Name, true)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		writeJSON(w, 200, pairing)
	default:
		http.NotFound(w, r)
	}
}
