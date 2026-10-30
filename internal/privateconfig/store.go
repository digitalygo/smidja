package privateconfig

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
)

var ErrNotFound = errors.New("private entity not found")
var ErrConflict = errors.New("private revision or lifecycle conflict")
var ErrClosed = errors.New("private configuration store closed")

const Retention = 30 * 24 * time.Hour

type Agent struct {
	Name         string   `json:"name"`
	Instructions string   `json:"instructions"`
	Model        string   `json:"model,omitempty"`
	Tools        []string `json:"tools,omitempty"`
}
type Team struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Agents      []Agent `json:"agents"`
}
type Extension struct {
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Parameters     json.RawMessage   `json:"parameters"`
	Command        []string          `json:"command"`
	ClientID       string            `json:"client_id,omitempty"`
}
type Change struct {
	Action       string          `json:"action"`
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	BaseRevision uint64          `json:"base_revision"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}
type Entity struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Revision   uint64          `json:"revision"`
	Payload    json.RawMessage `json:"payload"`
	ArchivedAt *time.Time      `json:"archived_at,omitempty"`
	ExpiresAt  *time.Time      `json:"expires_at,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
}
type Proposal struct {
	ID         string     `json:"id"`
	Change     Change     `json:"change"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}
type record struct {
	Version  int       `json:"version"`
	Sequence uint64    `json:"sequence"`
	Entity   *Entity   `json:"entity,omitempty"`
	Proposal *Proposal `json:"proposal,omitempty"`
	Purged   []string  `json:"purged,omitempty"`
}
type Store struct {
	mu        sync.Mutex
	file      *os.File
	seq       uint64
	entities  map[string]Entity
	proposals map[string]Proposal
	retired   map[string]bool
	failure   error
}

func Open(directory string) (*Store, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "private-config.jsonl")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	fail := func(err error) (*Store, error) { file.Close(); return nil, err }
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("private journal must be a regular file"))
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(err)
	}
	s := &Store{file: file, entities: map[string]Entity{}, retired: map[string]bool{}, proposals: map[string]Proposal{}}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			break
		}
		if err != nil {
			return fail(fmt.Errorf("incomplete private journal; preserve and inspect trailing record: %w", err))
		}
		var row record
		if err = decode(line, &row); err != nil {
			return fail(err)
		}
		if row.Version != 1 || row.Sequence != s.seq+1 || (row.Entity == nil && row.Proposal == nil && len(row.Purged) == 0) {
			return fail(errors.New("invalid private journal record"))
		}
		s.apply(row)
	}
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}
func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var result T
	_ = json.Unmarshal(b, &result)
	return result
}
func decode(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
func (s *Store) apply(row record) {
	if row.Entity != nil {
		s.entities[row.Entity.ID] = clone(*row.Entity)
	}
	if row.Proposal != nil {
		s.proposals[row.Proposal.ID] = clone(*row.Proposal)
	}
	for _, id := range row.Purged {
		delete(s.entities, id)
		s.retired[id] = true
	}
	s.seq = row.Sequence
}
func (s *Store) commit(row record) error {
	if s.file == nil {
		return ErrClosed
	}
	if s.failure != nil {
		return s.failure
	}
	row.Version = 1
	row.Sequence = s.seq + 1
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := s.file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.file.Sync()
	}
	if err != nil {
		s.failure = err
		return err
	}
	s.apply(row)
	return nil
}
func (s *Store) Get(id string) (Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entity, ok := s.entities[id]
	if !ok {
		return Entity{}, ErrNotFound
	}
	return clone(entity), nil
}
func (s *Store) List(kind string, includeArchived bool) []Entity {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Entity{}
	for _, entity := range s.entities {
		if (kind == "" || entity.Kind == kind) && (includeArchived || entity.ArchivedAt == nil) {
			result = append(result, clone(entity))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (s *Store) Pending() []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Proposal{}
	for _, proposal := range s.proposals {
		if proposal.Status == "pending" {
			result = append(result, clone(proposal))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}
func (s *Store) GetProposal(id string) (Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.proposals[id]
	if !ok {
		return Proposal{}, ErrNotFound
	}
	return clone(proposal), nil
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var privateID = regexp.MustCompile(`^private:[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validatePayload(kind string, payload json.RawMessage) error {
	if len(payload) > 1<<20 {
		return errors.New("private payload exceeds one MiB")
	}
	switch kind {
	case "team":
		var team Team
		if err := decode(payload, &team); err != nil {
			return err
		}
		if strings.TrimSpace(team.Name) == "" || len(team.Agents) == 0 {
			return errors.New("team name and agents required")
		}
		seen := map[string]bool{}
		for _, agent := range team.Agents {
			if strings.TrimSpace(agent.Name) == "" || seen[agent.Name] {
				return errors.New("agent names must be present and unique")
			}
			seen[agent.Name] = true
		}
	case "workflow":
		var workflow managerstate.Workflow
		if err := decode(payload, &workflow); err != nil {
			return err
		}
		if workflow.ID == "" || workflow.Version == "" || len(workflow.Phases) == 0 {
			return errors.New("workflow identity, version and phases required")
		}
		seen := map[string]bool{}
		for _, phase := range workflow.Phases {
			if phase.ID == "" || phase.TeamRef == "" || seen[phase.ID] {
				return errors.New("workflow phase identity and team required")
			}
			seen[phase.ID] = true
		}
	case "extension":
		var extension Extension
		if err := decode(payload, &extension); err != nil {
			return err
		}
		if _, err := ExtensionTimeout(extension.TimeoutSeconds); err != nil {
			return err
		}
		if strings.TrimSpace(extension.Name) == "" || len(extension.Command) == 0 || strings.TrimSpace(extension.Command[0]) == "" {
			return errors.New("extension name and command required")
		}
		for name, reference := range extension.Environment {
			if !environmentName.MatchString(name) || !environmentName.MatchString(reference) {
				return errors.New("extension environment must map variable names to environment variable names, not literal values")
			}
		}
		var schema map[string]any
		if err := json.Unmarshal(extension.Parameters, &schema); err != nil || schema == nil {
			return errors.New("extension parameters must be a JSON schema object")
		}
	default:
		return errors.New("private kind must be team, workflow or extension")
	}
	return nil
}
func (s *Store) prepare(change Change, now time.Time) (Entity, error) {
	if !privateID.MatchString(change.ID) {
		return Entity{}, errors.New("only private: entity IDs are mutable")
	}
	if change.Kind != "team" && change.Kind != "workflow" && change.Kind != "extension" {
		return Entity{}, errors.New("unsupported private entity kind")
	}
	old, exists := s.entities[change.ID]
	if change.Action == "create" {
		if exists || s.retired[change.ID] || change.BaseRevision != 0 {
			return Entity{}, ErrConflict
		}
	} else {
		if !exists {
			return Entity{}, ErrNotFound
		}
		if old.Kind != change.Kind || old.Revision != change.BaseRevision {
			return Entity{}, ErrConflict
		}
	}
	next := clone(old)
	next.ID = change.ID
	next.Kind = change.Kind
	next.Revision = change.BaseRevision + 1
	next.UpdatedAt = now
	switch change.Action {
	case "create", "update":
		if old.ArchivedAt != nil {
			return Entity{}, ErrConflict
		}
		if err := validatePayload(change.Kind, change.Payload); err != nil {
			return Entity{}, err
		}
		next.Payload = append(json.RawMessage(nil), change.Payload...)
	case "archive":
		if old.ArchivedAt != nil || len(change.Payload) > 0 {
			return Entity{}, ErrConflict
		}
		expires := now.Add(Retention)
		next.ArchivedAt = &now
		next.ExpiresAt = &expires
	case "restore":
		if old.ArchivedAt == nil || old.ExpiresAt == nil || !now.Before(*old.ExpiresAt) || len(change.Payload) > 0 {
			return Entity{}, ErrConflict
		}
		next.ArchivedAt = nil
		next.ExpiresAt = nil
	default:
		return Entity{}, errors.New("unsupported private change action")
	}
	return next, nil
}
func (s *Store) Propose(change Change) (Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if _, err := s.prepare(change, now); err != nil {
		return Proposal{}, err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return Proposal{}, err
	}
	proposal := Proposal{ID: hex.EncodeToString(random), Change: clone(change), Status: "pending", CreatedAt: now}
	if err := s.commit(record{Proposal: &proposal}); err != nil {
		return Proposal{}, err
	}
	return clone(proposal), nil
}
func (s *Store) ApplyOwner(change Change) (Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entity, err := s.prepare(change, time.Now().UTC())
	if err != nil {
		return Entity{}, err
	}
	if err = s.commit(record{Entity: &entity}); err != nil {
		return Entity{}, err
	}
	return clone(entity), nil
}
func (s *Store) Approve(id string) (Entity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.proposals[id]
	if !ok {
		return Entity{}, ErrNotFound
	}
	if proposal.Status != "pending" {
		return Entity{}, ErrConflict
	}
	now := time.Now().UTC()
	entity, err := s.prepare(proposal.Change, now)
	if err != nil {
		return Entity{}, err
	}
	proposal.Status = "approved"
	proposal.ResolvedAt = &now
	if err = s.commit(record{Entity: &entity, Proposal: &proposal}); err != nil {
		return Entity{}, err
	}
	return clone(entity), nil
}
func (s *Store) Reject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.proposals[id]
	if !ok {
		return ErrNotFound
	}
	if proposal.Status != "pending" {
		return ErrConflict
	}
	now := time.Now().UTC()
	proposal.Status = "rejected"
	proposal.ResolvedAt = &now
	return s.commit(record{Proposal: &proposal})
}
func (s *Store) PurgeExpired(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for id, entity := range s.entities {
		if entity.ExpiresAt != nil && !now.Before(*entity.ExpiresAt) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	sort.Strings(ids)
	if err := s.commit(record{Purged: ids}); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func ExtensionTimeout(seconds int) (time.Duration, error) {
	if seconds < 0 || seconds > 600 {
		return 0, errors.New("extension timeout_seconds must be zero (default 30) or between 1 and 600")
	}
	if seconds == 0 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second, nil
}
