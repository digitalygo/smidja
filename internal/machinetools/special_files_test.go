package machinetools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadRejectsWriterlessFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, link} {
		t.Run(filepath.Base(target), func(t *testing.T) {
			args, _ := json.Marshal(Arguments{Path: target})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan Result, 1)
			go func() { done <- New(Config{}).Execute(ctx, "read", args) }()
			select {
			case result := <-done:
				if result.OK || result.Error != "not a regular file" {
					t.Fatalf("Unexpected result: %+v", result)
				}
				if ctx.Err() != nil {
					t.Fatal("FIFO rejection waited for deadline")
				}
			case <-ctx.Done():
				writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
				if err == nil {
					writer.Close()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("Writerless FIFO blocked past deadline")
			}
		})
	}
}

func TestReadBytesContextCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(path, []byte("regular content"), 0600); err != nil {
		t.Fatal(err)
	}
	service := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.readBytesContext(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := service.readBytesContext(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if data, err := service.readBytesContext(context.Background(), path); err != nil || string(data) != "regular content" {
		t.Fatal(string(data), err)
	}
}

type cancellingReader struct{ cancel context.CancelFunc }

func (r cancellingReader) Read(p []byte) (int, error) { r.cancel(); copy(p, "x"); return 1, nil }
func TestContextReaderCancellationDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := contextReader{ctx, cancellingReader{cancel}}
	if _, err := io.ReadAll(reader); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reader = contextReader{ctx, strings.NewReader("unused")}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
