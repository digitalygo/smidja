package machineclient

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
)

func TestWriterlessFIFOLeavesClientUsable(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	regular := filepath.Join(dir, "regular")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regular, []byte("second operation"), 0600); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(BrokerConfig{PollTimeout: 20 * time.Millisecond, ExecutionTimeout: time.Second})
	if err := broker.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, clientConfig(server.URL), machinetools.New(machinetools.Config{})) }()
	defer func() {
		cancel()
		writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if err == nil {
			writer.Close()
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("client failed to stop")
		}
	}()
	until := time.Now().Add(time.Second)
	for !broker.List()[0].Online && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	args, _ := json.Marshal(machinetools.Arguments{Path: fifo})
	result := broker.Execute(ctx, "laptop", "read", args)
	if result.OK || result.Error != "not a regular file" {
		t.Fatalf("FIFO result: %+v", result)
	}
	args, _ = json.Marshal(machinetools.Arguments{Path: regular})
	result = broker.Execute(ctx, "laptop", "read", args)
	if !result.OK {
		t.Fatalf("Second operation failed: %+v", result)
	}
	data, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var file machinetools.File
	if err = json.Unmarshal(data, &file); err != nil || file.Content != "second operation" {
		t.Fatal(string(data), err)
	}
}
