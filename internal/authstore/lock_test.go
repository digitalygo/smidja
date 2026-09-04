package authstore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func assertNoTempFiles(t *testing.T, path string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".auth-*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files: %v", matches)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != want {
		t.Errorf("%s perm = %o, want %o", path, perm, want)
	}
}

func TestSetMergesFreshDiskState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	first, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := first.Set("first", Entry{Type: "api_key", Key: "k1"}); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := second.Set("second", Entry{Type: "api_key", Key: "k2"}); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for provider, want := range map[string]string{"first": "k1", "second": "k2"} {
		e, ok := reloaded.Get(provider)
		if !ok || e.Type != "api_key" || e.Key != want {
			t.Errorf("reloaded %s = %+v, %v; want api_key with key %q", provider, e, ok, want)
		}
	}
}

func TestRemoveRefreshesFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	observer, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	writer, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := writer.Set("target", Entry{Type: "api_key", Key: "k1"}); err != nil {
		t.Fatalf("Set target: %v", err)
	}
	if err := writer.Set("keeper", Entry{Type: "api_key", Key: "k2"}); err != nil {
		t.Fatalf("Set keeper: %v", err)
	}
	if _, ok := observer.Get("target"); ok {
		t.Fatal("observer must start stale for this test")
	}
	if err := observer.Remove("target"); err != nil {
		t.Fatalf("Remove of a provider missing from local memory: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reloaded.Get("target"); ok {
		t.Error("target survived Remove from a stale store")
	}
	e, ok := reloaded.Get("keeper")
	if !ok || e.Key != "k2" {
		t.Errorf("keeper = %+v, %v; want k2 preserved on disk", e, ok)
	}
	if _, ok := observer.Get("target"); ok {
		t.Error("removed provider still present in memory")
	}
	if _, ok := observer.Get("keeper"); !ok {
		t.Error("Remove did not refresh memory from disk")
	}
}

func TestRemoveNoopRefreshesMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	observer, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	writer, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := writer.Set("keeper", Entry{Type: "api_key", Key: "k2"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := observer.Remove("missing"); err != nil {
		t.Fatalf("Remove of unknown provider: %v", err)
	}
	if e, ok := observer.Get("keeper"); !ok || e.Key != "k2" {
		t.Errorf("Get after noop Remove = %+v, %v; want the disk entry", e, ok)
	}
}

func TestSetRemoveContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	const workers = 8
	const ops = 25
	for r := 0; r < workers; r++ {
		if err := s.Set(fmt.Sprintf("removable-%d", r), Entry{Type: "api_key", Key: "seed"}); err != nil {
			t.Fatalf("seed removable-%d: %v", r, err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2*workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			provider := fmt.Sprintf("writer-%d", w)
			key := strings.Repeat(string(rune('a'+w)), 8)
			for i := 0; i < ops; i++ {
				if err := s.Set(provider, Entry{Type: "api_key", Key: key}); err != nil {
					errs <- fmt.Errorf("Set %s: %w", provider, err)
					return
				}
			}
		}(w)
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			provider := fmt.Sprintf("removable-%d", w)
			for i := 0; i < ops; i++ {
				if err := s.Remove(provider); err != nil {
					errs <- fmt.Errorf("Remove %s: %w", provider, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload after contention: %v", err)
	}
	for w := 0; w < workers; w++ {
		e, ok := reloaded.Get(fmt.Sprintf("writer-%d", w))
		if !ok || e.Type != "api_key" || len(e.Key) != 8 {
			t.Errorf("writer-%d = %+v, %v; want a surviving 8-char api_key", w, e, ok)
		}
		if _, ok := reloaded.Get(fmt.Sprintf("removable-%d", w)); ok {
			t.Errorf("removable-%d survived concurrent removes", w)
		}
	}
	assertPerm(t, path, 0o600)
	assertNoTempFiles(t, path)
}

func TestSetRefusesMalformedCurrentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	corrupt := `{not json`
	writeAuthFile(t, path, corrupt)
	if err := s.Set("p", Entry{Type: "api_key", Key: "k"}); err == nil {
		t.Fatal("Set on a malformed disk file: want error")
	} else if !strings.Contains(err.Error(), "invalid auth.json") {
		t.Errorf("err = %v, want the malformed auth.json error", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != corrupt {
		t.Errorf("malformed file was overwritten with %q", content)
	}
	if _, ok := s.Get("p"); ok {
		t.Error("in-memory store accepted the refused write")
	}
	assertNoTempFiles(t, path)
}

func TestRemoveRefusesMalformedCurrentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Set("p", Entry{Type: "api_key", Key: "k"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	corrupt := `{"p": broken`
	writeAuthFile(t, path, corrupt)
	if err := s.Remove("p"); err == nil {
		t.Fatal("Remove on a malformed disk file: want error")
	} else if !strings.Contains(err.Error(), "invalid auth.json") {
		t.Errorf("err = %v, want the malformed auth.json error", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != corrupt {
		t.Errorf("malformed file was overwritten with %q", content)
	}
	if e, ok := s.Get("p"); !ok || e.Key != "k" {
		t.Errorf("Get after refused Remove = %+v, %v; want the in-memory entry kept", e, ok)
	}
	assertNoTempFiles(t, path)
}

func TestSetRemovePreservePermissionsAndCleanupTemps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Set("p", Entry{Type: "api_key", Key: "k1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	assertPerm(t, filepath.Dir(path), 0o700)
	assertPerm(t, path, 0o600)
	assertNoTempFiles(t, path)

	if err := s.Remove("p"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertPerm(t, path, 0o600)
	assertNoTempFiles(t, path)

	wide := filepath.Join(dir, "wide.json")
	if err := os.WriteFile(wide, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write permissive fixture: %v", err)
	}
	permissive, err := Load(wide)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := permissive.Set("p", Entry{Type: "api_key", Key: "k2"}); err != nil {
		t.Fatalf("Set on permissive file: %v", err)
	}
	assertPerm(t, wide, 0o600)
}

func TestSetFailsWhenDirectoryCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "sub")
	path := filepath.Join(storeDir, "auth.json")
	writeAuthFile(t, path, "{}")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.RemoveAll(storeDir); err != nil {
		t.Fatalf("remove store dir: %v", err)
	}
	if err := os.WriteFile(storeDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if err := s.Set("p", Entry{Type: "api_key", Key: "k"}); err == nil {
		t.Fatal("Set with an uncreatable directory: want error")
	} else if !strings.Contains(err.Error(), "authstore: create") {
		t.Errorf("err = %v, want the directory creation error", err)
	}
}

func TestSetFailsWhenLockFileCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are bypassed as root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := s.Set("p", Entry{Type: "api_key", Key: "k"}); err == nil {
		t.Fatal("Set with an uncreatable lock file: want error")
	} else if !strings.Contains(err.Error(), "lock file") {
		t.Errorf("err = %v, want the lock file error", err)
	}
}

func TestAcquireFileLockSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json.lock")
	first, err := acquireFileLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	acquired := make(chan *fileLock, 1)
	acquireErr := make(chan error, 1)
	go func() {
		second, err := acquireFileLock(path)
		if err != nil {
			acquireErr <- err
			return
		}
		acquired <- second
	}()
	if err := first.release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	select {
	case second := <-acquired:
		if err := second.release(); err != nil {
			t.Fatalf("second release: %v", err)
		}
	case err := <-acquireErr:
		t.Fatalf("second acquire after first release: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("second acquire never completed")
	}
}

const staleWriterEnv = "SMIDJA_AUTHSTORE_STALE_WRITER"

func TestSetStaleWriterSubprocess(t *testing.T) {
	if os.Getenv(staleWriterEnv) == "1" {
		runStaleWriterChild()
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	seed, err := Load(path)
	if err != nil {
		t.Fatalf("seed Load: %v", err)
	}
	if err := seed.Set("seed", Entry{Type: "api_key", Key: "seed-key"}); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	const children = 4
	readyDir := filepath.Join(dir, "ready")
	doneDir := filepath.Join(dir, "done")
	if err := os.MkdirAll(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(doneDir, 0o700); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(dir, "go")

	var cmds []*exec.Cmd
	for i := 0; i < children; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSetStaleWriterSubprocess$")
		cmd.Env = append(os.Environ(),
			staleWriterEnv+"=1",
			"SMIDJA_AUTHSTORE_PATH="+path,
			"SMIDJA_AUTHSTORE_INDEX="+strconv.Itoa(i),
			"SMIDJA_AUTHSTORE_READY="+filepath.Join(readyDir, strconv.Itoa(i)),
			"SMIDJA_AUTHSTORE_DONE="+filepath.Join(doneDir, strconv.Itoa(i)),
			"SMIDJA_AUTHSTORE_GO="+goFile,
		)
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatalf("start child: %v", err)
		}
	}
	var wg sync.WaitGroup
	for _, cmd := range cmds {
		wg.Add(1)
		go func(cmd *exec.Cmd) {
			defer wg.Done()
			_ = cmd.Wait()
		}(cmd)
	}
	t.Cleanup(func() {
		for _, cmd := range cmds {
			_ = cmd.Process.Kill()
		}
		wg.Wait()
	})

	waitForMarker := func(path, want, what string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		var content []byte
		for {
			content, _ = os.ReadFile(path)
			if string(content) == want {
				return
			}
			if strings.HasPrefix(string(content), "err:") {
				t.Fatalf("%s: %s", what, content)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never reached %q (last content %q)", what, want, content)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for i := 0; i < children; i++ {
		waitForMarker(filepath.Join(readyDir, strconv.Itoa(i)), "ok", fmt.Sprintf("ready marker for child %d", i))
	}
	if err := os.WriteFile(goFile, []byte("go"), 0o600); err != nil {
		t.Fatalf("write go barrier: %v", err)
	}
	for i := 0; i < children; i++ {
		waitForMarker(filepath.Join(doneDir, strconv.Itoa(i)), "ok", fmt.Sprintf("done marker for child %d", i))
	}
	wg.Wait()

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload after concurrent subprocess writes: %v", err)
	}
	e, ok := reloaded.Get("seed")
	if !ok || e.Key != "seed-key" {
		t.Errorf("seed = %+v, %v; want seed-key preserved", e, ok)
	}
	for i := 0; i < children; i++ {
		e, ok := reloaded.Get(fmt.Sprintf("p-%d", i))
		if !ok || e.Type != "api_key" || e.Key != fmt.Sprintf("key-%d", i) {
			t.Errorf("child %d write lost: entry = %+v, present = %v", i, e, ok)
		}
	}
	assertNoTempFiles(t, path)
}

func runStaleWriterChild() {
	path := os.Getenv("SMIDJA_AUTHSTORE_PATH")
	index := os.Getenv("SMIDJA_AUTHSTORE_INDEX")
	readyPath := os.Getenv("SMIDJA_AUTHSTORE_READY")
	donePath := os.Getenv("SMIDJA_AUTHSTORE_DONE")
	goPath := os.Getenv("SMIDJA_AUTHSTORE_GO")
	fail := func(format string, args ...any) {
		_ = os.WriteFile(readyPath, []byte("err: "+fmt.Sprintf(format, args...)), 0o600)
		os.Exit(1)
	}
	s, err := Load(path)
	if err != nil {
		fail("load: %v", err)
	}
	if err := os.WriteFile(readyPath, []byte("ok"), 0o600); err != nil {
		os.Exit(1)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(goPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = os.WriteFile(donePath, []byte("err: go barrier never opened"), 0o600)
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Set("p-"+index, Entry{Type: "api_key", Key: "key-" + index}); err != nil {
		_ = os.WriteFile(donePath, []byte("err: "+err.Error()), 0o600)
		os.Exit(1)
	}
	_ = os.WriteFile(donePath, []byte("ok"), 0o600)
	os.Exit(0)
}
