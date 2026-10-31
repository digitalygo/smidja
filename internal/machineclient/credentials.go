package machineclient

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

type credentialRecord struct {
	Version  int    `json:"version"`
	Sequence uint64 `json:"sequence"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hash     string `json:"sha256"`
	Revoked  bool   `json:"revoked"`
}
type credentialJournal struct {
	file     *os.File
	sequence uint64
	records  map[string]credentialRecord
}
type Pairing struct {
	ClientID string `json:"client_id"`
	Name     string `json:"name"`
	Token    string `json:"token"`
}

var pairingID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func openCredentials(path string) (*credentialJournal, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("client credential journal path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	fail := func(err error) (*credentialJournal, error) { file.Close(); return nil, err }
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("client credential journal must be regular"))
	}
	if err = file.Chmod(0600); err != nil {
		return fail(err)
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(errors.New("client credential journal already open"))
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return fail(err)
	}
	journal := &credentialJournal{file: file, records: map[string]credentialRecord{}}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if err != nil {
			return fail(errors.New("incomplete client credential journal; preserve and inspect before recovery"))
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var record credentialRecord
		if decoder.Decode(&record) != nil {
			return fail(errors.New("invalid client credential journal record"))
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF || record.Version != 1 || record.Sequence != journal.sequence+1 || record.ID == "manager" || strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Name) == "" {
			return fail(errors.New("invalid client credential journal identity or sequence"))
		}
		hash, err := hex.DecodeString(record.Hash)
		if err != nil || len(hash) != sha256.Size {
			return fail(errors.New("invalid client credential hash"))
		}
		journal.sequence = record.Sequence
		journal.records[record.ID] = record
	}
	return journal, nil
}
func (j *credentialJournal) append(record credentialRecord) error {
	if j.file == nil {
		return errors.New("client credential journal is closed")
	}
	record.Version = 1
	record.Sequence = j.sequence + 1
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = j.file.Write(append(data, '\n')); err == nil {
		err = j.file.Sync()
	}
	if err != nil {
		return err
	}
	j.sequence = record.Sequence
	j.records[record.ID] = record
	return nil
}
func (b *Broker) loadCredentials() error {
	if b.cfg.CredentialsPath == "" {
		return nil
	}
	journal, err := openCredentials(b.cfg.CredentialsPath)
	if err != nil {
		return err
	}
	b.credentials = journal
	for id, record := range journal.records {
		if record.Revoked {
			delete(b.inventory, id)
			continue
		}
		decoded, _ := hex.DecodeString(record.Hash)
		var hash [sha256.Size]byte
		copy(hash[:], decoded)
		b.addClientHash(id, record.Name, hash)
	}
	return nil
}
func (b *Broker) addClientHash(id, name string, hash [sha256.Size]byte) {
	status := b.inventory[id]
	status.ID, status.Name, status.Online, status.Revoked = id, name, false, false
	b.clients[id] = &registeredClient{status: status, tokenHash: hash, queue: make(chan string, b.cfg.MaxPending), revoked: make(chan struct{}), cancelled: map[string]struct{}{}}
}
func (b *Broker) PairClient(id, name string, rotate bool) (Pairing, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.initErr != nil {
		return Pairing{}, b.initErr
	}
	if b.credentials == nil || b.credentials.file == nil {
		return Pairing{}, errors.New("durable client pairing is not configured")
	}
	if !pairingID.MatchString(id) || id == "manager" || strings.TrimSpace(name) == "" || len(name) > 256 {
		return Pairing{}, errors.New("valid client ID and display name required")
	}
	_, live := b.clients[id]
	_, known := b.credentials.records[id]
	if rotate && !live && !known {
		return Pairing{}, errors.New("client does not exist; create it first")
	}
	if !rotate && (live || known) {
		return Pairing{}, errors.New("client ID already exists; explicitly rotate credentials if the secret was lost")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Pairing{}, err
	}
	token := hex.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	record := credentialRecord{ID: id, Name: name, Hash: hex.EncodeToString(hash[:])}
	if err := b.credentials.append(record); err != nil {
		b.initErr = err
		return Pairing{}, errors.New("client credential outcome uncertain; inspect the journal before another change")
	}
	if live {
		b.revokeMemory(id)
	}
	b.addClientHash(id, name, hash)
	return Pairing{ClientID: id, Name: name, Token: token}, nil
}
func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.initErr = errors.New("client broker is closed")
	for id := range b.clients {
		b.revokeMemory(id)
	}
	if b.credentials == nil || b.credentials.file == nil {
		return nil
	}
	err := b.credentials.file.Close()
	b.credentials.file = nil
	return err
}
func (b *Broker) PairingReady() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.initErr == nil && b.credentials != nil && b.credentials.file != nil
}
