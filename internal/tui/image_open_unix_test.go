//go:build linux || darwin

package tui

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestImageOpenMissingComponentIsRejected(t *testing.T) {
	root := t.TempDir()
	if _, err := NewImageLoader(root, true).Load("missing/pic.png"); err != ErrImageNotRegular {
		t.Fatalf("missing component = %v, want %v", err, ErrImageNotRegular)
	}
	writePNG(t, filepath.Join(root, "plain.png"), 2, 2)
	if _, err := NewImageLoader(root, true).Load("plain.png/pic.png"); err != ErrImageNotRegular {
		t.Fatalf("file as directory = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageOpenMissingRootIsRejected(t *testing.T) {
	loader := NewImageLoader(filepath.Join(t.TempDir(), "missing-root"), true)
	if _, err := loader.Load("pic.png"); err != ErrImageUnsafePath {
		t.Fatalf("missing root = %v, want %v", err, ErrImageUnsafePath)
	}
}

func TestImageOpenHookErrorPropagates(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "pic.png"), 2, 2)
	sentinel := errors.New("hook stop")
	loader := NewImageLoader(root, true)
	loader.openHook = func(stage imageOpenStage, path string) error {
		if stage == imageOpenStageRoot {
			return sentinel
		}
		return nil
	}
	if _, err := loader.Load("pic.png"); !errors.Is(err, sentinel) {
		t.Fatalf("hook error = %v, want %v", err, sentinel)
	}
}

func TestImageOpenFinalRemovedAtValidatedHook(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "pic.png")
	writePNG(t, final, 2, 2)
	loader := NewImageLoader(root, true)
	loader.openHook = func(stage imageOpenStage, path string) error {
		if stage == imageOpenStageValidated && path == final {
			return os.Remove(final)
		}
		return nil
	}
	if _, err := loader.Load("pic.png"); err != ErrImageNotRegular {
		t.Fatalf("removed final = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageLoaderRejectsIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(real, "pic.png"), 4, 4)
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("link/pic.png"); err != ErrImageNotRegular {
		t.Fatalf("intermediate symlink = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageLoaderRejectsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	if err := os.Mkdir(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(outside, "pic.png"), 4, 4)
	if err := os.Symlink(outside, filepath.Join(outer, "inner")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("outer/inner/pic.png"); err != ErrImageNotRegular {
		t.Fatalf("symlinked ancestor = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageOpenIntermediateRaceHook(t *testing.T) {
	root := t.TempDir()
	pics := filepath.Join(root, "pics")
	if err := os.Mkdir(pics, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(pics, "pic.png"), 4, 4)
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(other, "pic.png"), 2, 2)
	loader := NewImageLoader(root, true)
	loader.openHook = func(stage imageOpenStage, path string) error {
		if stage != imageOpenStageComponent || path != pics {
			return nil
		}
		if err := os.Rename(pics, filepath.Join(root, "pics-moved")); err != nil {
			return err
		}
		return os.Symlink("other", pics)
	}
	if _, err := loader.Load("pics/pic.png"); err != ErrImageNotRegular {
		t.Fatalf("swapped intermediate = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageOpenFinalFIFORaceHookIsNonblocking(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "pic.png")
	writePNG(t, final, 4, 4)
	loader := NewImageLoader(root, true)
	loader.openHook = func(stage imageOpenStage, path string) error {
		if stage != imageOpenStageValidated || path != final {
			return nil
		}
		if err := os.Remove(final); err != nil {
			return err
		}
		return syscall.Mkfifo(final, 0o644)
	}
	done := make(chan error, 1)
	go func() {
		_, err := loader.Load("pic.png")
		done <- err
	}()
	select {
	case err := <-done:
		if err != ErrImageNotRegular {
			t.Fatalf("fifo race = %v, want %v", err, ErrImageNotRegular)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening the replaced FIFO blocked; the descriptor is not nonblocking")
	}
}

func TestImageOpenFinalSymlinkRaceHook(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "pic.png")
	writePNG(t, final, 4, 4)
	writePNG(t, filepath.Join(root, "other.png"), 2, 2)
	loader := NewImageLoader(root, true)
	loader.openHook = func(stage imageOpenStage, path string) error {
		if stage != imageOpenStageValidated || path != final {
			return nil
		}
		if err := os.Remove(final); err != nil {
			return err
		}
		return os.Symlink("other.png", final)
	}
	if _, err := loader.Load("pic.png"); err != ErrImageNotRegular {
		t.Fatalf("final symlink race = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageOpenFinalSwapAfterHoldHook(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "pic.png")
	writePNG(t, final, 4, 4)
	loader := NewImageLoader(root, true)
	loader.openHook = func(stage imageOpenStage, path string) error {
		if stage != imageOpenStageHeld || path != final {
			return nil
		}
		if err := os.Remove(final); err != nil {
			return err
		}
		writePNG(t, final, 2, 2)
		return nil
	}
	if _, err := loader.Load("pic.png"); err != ErrImageNotRegular {
		t.Fatalf("path replacement = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageOpenDescriptorIsNonblocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	file, err := openImageDescriptor(directory, "pipe", fifo, nil)
	if err != nil {
		t.Fatalf("open fifo: %v", err)
	}
	defer file.Close()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), uintptr(syscall.F_GETFL), 0)
	if errno != 0 {
		t.Fatalf("fcntl(F_GETFL): %v", errno)
	}
	if flags&uintptr(syscall.O_NONBLOCK) == 0 {
		t.Fatalf("descriptor flags %#x are missing O_NONBLOCK", flags)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("fifo descriptor mode = %v", info.Mode())
	}
}

func TestImageLoaderRejectsSocket(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := NewImageLoader(root, true).Load("sock"); err != ErrImageNotRegular {
		t.Fatalf("socket = %v, want %v", err, ErrImageNotRegular)
	}
}

func TestImageLoaderDisabledPerformsNoFilesystemOperations(t *testing.T) {
	loader := NewImageLoader(filepath.Join(t.TempDir(), "missing-root"), false)
	calls := 0
	loader.openHook = func(stage imageOpenStage, path string) error {
		calls++
		return nil
	}
	loader.SetOpenFile(func(path string) (*os.File, error) {
		calls++
		return nil, os.ErrPermission
	})
	candidates := []string{"pic.png", "dir/pic.png", "http://example.com/pic.png", "data:image/png;base64,AAAA", "../escape.png", "/etc/passwd"}
	for _, candidate := range candidates {
		if _, err := loader.Load(candidate); err != ErrImageDisabled {
			t.Fatalf("disabled load %q = %v, want %v", candidate, err, ErrImageDisabled)
		}
	}
	if calls != 0 {
		t.Fatalf("disabled loader performed %d filesystem operations", calls)
	}
	if _, err := loader.ResolvePath("pic.png"); err != nil {
		t.Fatalf("lexical resolution should work without filesystem access: %v", err)
	}
}

func TestImageOpenRejectsAbsoluteAndRemotePaths(t *testing.T) {
	loader := NewImageLoader(t.TempDir(), true)
	candidates := []string{"//host/share", "file:///etc/passwd", "ftp://host/x.png", "sub/..", "sub/../../x.png", "a/../b.png", "."}
	for _, candidate := range candidates {
		if _, err := loader.Load(candidate); err != ErrImageUnsafePath {
			t.Fatalf("path %q = %v, want %v", candidate, err, ErrImageUnsafePath)
		}
	}
}
