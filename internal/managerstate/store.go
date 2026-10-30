package managerstate

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type Store struct {
	tools         map[string]ToolOperation
	mu            sync.Mutex
	file          *os.File
	seq           uint64
	tasks         map[string]Task
	sessions      map[string]Session
	invocations   map[string]string
	events        map[string]bool
	recoveredTail bool
	failure       error
}

func Open(directory string) (*Store, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "manager.jsonl")
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("journal must be a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("manager state already open: %w", err)
	}
	s := &Store{tools: map[string]ToolOperation{}, file: file, tasks: map[string]Task{}, sessions: map[string]Session{}, invocations: map[string]string{}, events: map[string]bool{}}
	if err = s.replay(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) replay() error {
	data, err := io.ReadAll(s.file)
	if err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		offset := bytes.LastIndexByte(data, '\n') + 1
		tail := data[offset:]
		backup := s.file.Name() + ".incomplete-" + newID()
		if err = os.WriteFile(backup, tail, 0600); err != nil {
			return err
		}
		if err = s.file.Truncate(int64(offset)); err != nil {
			return err
		}
		if err = s.file.Sync(); err != nil {
			return err
		}
		data = data[:offset]
		s.recoveredTail = true
	}
	lines := bytes.Split(data, []byte{'\n'})
	for index, line := range lines {
		if index == len(lines)-1 && len(line) == 0 {
			break
		}
		var e event
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&e); err != nil {
			return fmt.Errorf("journal line %d: %w", index+1, err)
		}
		var extra any
		if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return fmt.Errorf("journal line %d has trailing data", index+1)
		}
		if e.SchemaVersion != 1 || e.Seq != s.seq+1 || e.ID == "" || s.events[e.ID] || e.At.IsZero() {
			return fmt.Errorf("journal line %d has invalid event identity", index+1)
		}
		apply, reduceErr := s.reduce(e)
		if reduceErr != nil {
			return fmt.Errorf("journal line %d: %w", index+1, reduceErr)
		}
		apply()
		s.seq = e.Seq
		s.events[e.ID] = true
	}
	_, err = s.file.Seek(0, io.SeekEnd)
	return err
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func clone[T any](value T) T {
	data, _ := json.Marshal(value)
	var result T
	_ = json.Unmarshal(data, &result)
	return result
}

func (s *Store) commit(e event) error {
	if s.file == nil {
		return ErrClosed
	}
	if s.failure != nil {
		return s.failure
	}
	e.SchemaVersion = 1
	e.ID = newID()
	e.Seq = s.seq + 1
	e.At = time.Now().UTC()
	apply, err := s.reduce(e)
	if err != nil {
		return err
	}
	data, err := json.Marshal(e)
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
		s.failure = fmt.Errorf("journal write failed; reopen required: %w", err)
		return s.failure
	}
	apply()
	s.seq = e.Seq
	s.events[e.ID] = true
	return nil
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

func (s *Store) RecoveredIncompleteTail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recoveredTail
}

func (s *Store) GetTask(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return clone(t), nil
}

func (s *Store) ListTasks() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		result = append(result, clone(t))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (s *Store) CurrentContext(id string) (PhaseContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return PhaseContext{}, ErrNotFound
	}
	result := PhaseContext{Inputs: append(json.RawMessage(nil), t.Inputs...), Focus: t.Focus, Clarifications: append([]string(nil), t.Clarifications...), Outputs: []PhaseOutput{}}
	for _, phase := range t.Phases {
		for _, attempt := range phase.Attempts {
			if attempt.OutputRecorded {
				result.Outputs = append(result.Outputs, PhaseOutput{PhaseID: phase.Phase.ID, Attempt: attempt.Number, Output: attempt.Output, Decision: attempt.Decision})
			}
		}
	}
	return result, nil
}
