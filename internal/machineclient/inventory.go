package machineclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (b *Broker) Err() error { b.mu.Lock(); defer b.mu.Unlock(); return b.initErr }

func (b *Broker) loadInventory() error {
	if b.cfg.InventoryPath == "" {
		return nil
	}
	f, err := os.Open(b.cfg.InventoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open client inventory: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return fmt.Errorf("read client inventory: %w", err)
	}
	if len(raw) > 16<<20 {
		return errors.New("client inventory exceeds 16 MiB")
	}
	var statuses []ClientStatus
	if err = json.Unmarshal(raw, &statuses); err != nil {
		return fmt.Errorf("decode client inventory: %w", err)
	}
	for _, status := range statuses {
		if status.ID == "" {
			return errors.New("client inventory contains empty ID")
		}
		if _, exists := b.inventory[status.ID]; exists {
			return errors.New("client inventory contains duplicate ID")
		}
		status.Online = false
		b.inventory[status.ID] = status
	}
	return nil
}

func (b *Broker) saveInventory() error {
	if b.cfg.InventoryPath == "" {
		return nil
	}
	statuses := make([]ClientStatus, 0, len(b.inventory))
	for _, status := range b.inventory {
		status.Online = false
		statuses = append(statuses, status)
	}
	raw, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	dir := filepath.Dir(b.cfg.InventoryPath)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".client-inventory-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, b.cfg.InventoryPath)
}
