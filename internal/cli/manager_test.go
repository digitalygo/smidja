package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serviceDeps() *Deps {
	return &Deps{Context: context.Background(), Env: func(key string) string {
		switch key {
		case "TOKEN":
			return strings.Repeat("t", 32)
		case "KEY":
			return "private-api-key"
		case "CLIENT":
			return strings.Repeat("c", 32)
		case "HOME":
			return "/tmp"
		case "PATH":
			return "/usr/bin:/bin"
		}
		return ""
	}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
}
func validManagerConfig(t *testing.T) managerFileConfig {
	t.Helper()
	return managerFileConfig{Listen: "127.0.0.1:0", StateDir: t.TempDir(), TokenEnv: "TOKEN", EnvaiKeyEnv: "KEY", EnvaiURL: "http://127.0.0.1:1234", WorkspaceID: "workspace", CEOTeamID: "ceo", Clients: []clientIdentity{{"laptop", "Laptop", "CLIENT"}}}
}
func configPath(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestManagerCLIConfigAndEnvironment(t *testing.T) {
	d := serviceDeps()
	cfg := validManagerConfig(t)
	path := configPath(t, cfg)
	var loaded managerFileConfig
	if err := readServiceConfig([]string{"--config", path}, "manager", d, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.CEOTeamID != "ceo" {
		t.Fatal("config lost")
	}
	environment := strings.Join(serviceEnvironment(d), "\n")
	if strings.Contains(environment, "private") || strings.Contains(environment, "TOKEN") || !strings.Contains(environment, "HOME=/tmp") {
		t.Fatal("unsafe shell environment", environment)
	}
	for _, test := range []struct {
		key string
		min int
	}{{"", 1}, {"MISSING", 1}, {"KEY", 100}} {
		if _, err := referencedSecret(d, test.key, "secret", test.min); err == nil {
			t.Fatal("bad secret accepted")
		}
	}
	malformed := *d
	malformed.Env = func(string) string { return "secret\nheader" }
	if _, err := referencedSecret(&malformed, "KEY", "key", 1); err == nil {
		t.Fatal("newline secret accepted")
	}
	for _, args := range [][]string{nil, {"--config", path, "extra"}, {"--config", "/nonexistent/config"}, {"--invalid"}} {
		if err := readServiceConfig(args, "manager", d, &loaded); err == nil {
			t.Fatal("invalid args", args)
		}
	}
	for _, data := range []string{`{"token":"inline-secret"}`, `{} {}`, `{`} {
		bad := filepath.Join(t.TempDir(), "bad.json")
		os.WriteFile(bad, []byte(data), 0600)
		if err := readServiceConfig([]string{"--config", bad}, "manager", d, &loaded); err == nil {
			t.Fatal("bad configuration accepted")
		}
	}
}
func TestManagerListenPolicy(t *testing.T) {
	cfg := validManagerConfig(t)
	cfg.Listen = ""
	if err := validateManagerListen(&cfg); err != nil || cfg.Listen != "127.0.0.1:8787" {
		t.Fatal(cfg, err)
	}
	for _, address := range []string{"0.0.0.0:8787", ":8787", "example.org:8787", "malformed"} {
		cfg.Listen = address
		if validateManagerListen(&cfg) == nil {
			t.Fatal("unsafe bind", address)
		}
	}
	for _, address := range []string{"127.0.0.1:0", "[::1]:8787", "localhost:8787"} {
		cfg.Listen = address
		if err := validateManagerListen(&cfg); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Listen = "0.0.0.0:8787"
	cfg.BehindTLSProxy = true
	if err := validateManagerListen(&cfg); err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = "relative"
	if validateManagerListen(&cfg) == nil {
		t.Fatal("relative state accepted")
	}
}
func TestManagerBuilderAndValidation(t *testing.T) {
	d := serviceDeps()
	cfg := validManagerConfig(t)
	server, store, err := buildManager(cfg, d)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	store.Close()
	mutations := []func(*managerFileConfig){
		func(c *managerFileConfig) { c.StateDir = "relative" },
		func(c *managerFileConfig) { c.TokenEnv = "missing" },
		func(c *managerFileConfig) { c.EnvaiKeyEnv = "missing" },
		func(c *managerFileConfig) { c.WorkspaceID = "" },
		func(c *managerFileConfig) { c.CEOTeamID = "" },
		func(c *managerFileConfig) { c.EnvaiURL = "invalid" },
		func(c *managerFileConfig) { c.Clients = []clientIdentity{{"laptop", "Laptop", "missing"}} },
		func(c *managerFileConfig) { c.Clients = []clientIdentity{{"laptop", "Laptop", "TOKEN"}} },
		func(c *managerFileConfig) { c.Clients = []clientIdentity{{"", "Laptop", "CLIENT"}} },
		func(c *managerFileConfig) { c.StateDir = configPath(t, map[string]string{}) },
	}
	for _, mutate := range mutations {
		bad := cfg
		mutate(&bad)
		s, st, err := buildManager(bad, d)
		if err == nil {
			s.Close()
			st.Close()
			t.Fatal("bad config accepted", bad)
		}
	}
}
func TestManagerCommandShutdown(t *testing.T) {
	d := serviceDeps()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Context = ctx
	if err := runManager([]string{"--config", configPath(t, validManagerConfig(t))}, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Stdout.(*bytes.Buffer).String(), "listening") {
		t.Fatal("no listen message")
	}
	for _, command := range []string{"manager", "client"} {
		if !isSubcommand(command) {
			t.Fatal("not registered", command)
		}
		if err := runSubcommand(command, []string{"--help"}, d); err != nil {
			t.Fatal(err)
		}
		if err := runSubcommand(command, nil, d); err == nil {
			t.Fatal("missing config accepted")
		}
	}
	cfg := validManagerConfig(t)
	cfg.Listen = "unsafe"
	if err := runManager([]string{"--config", configPath(t, cfg)}, d); err == nil {
		t.Fatal("bad listen accepted")
	}
	cfg = validManagerConfig(t)
	cfg.TokenEnv = "missing"
	if err := runManager([]string{"--config", configPath(t, cfg)}, d); err == nil {
		t.Fatal("missing secret accepted")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg = validManagerConfig(t)
	cfg.Listen = listener.Addr().String()
	if err = runManager([]string{"--config", configPath(t, cfg)}, d); err == nil {
		t.Fatal("occupied address accepted")
	}
}
func TestServeManagerLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveManager(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	}()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "ok" {
		t.Fatal("HTTP service failed")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	broken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	broken.Close()
	if err = serveManager(context.Background(), broken, http.NotFoundHandler()); err == nil {
		t.Fatal("listener failure hidden")
	}
}

type serviceRoundTrip func(*http.Request) (*http.Response, error)

func (f serviceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestClientCommandValidationAndCancellation(t *testing.T) {
	d := serviceDeps()
	cfg := clientFileConfig{ManagerURL: "http://127.0.0.1:1234", ClientID: "laptop", Name: "Laptop", TokenEnv: "CLIENT"}
	cfg.TokenEnv = "missing"
	if err := runMachineClient([]string{"--config", configPath(t, cfg)}, d); err == nil {
		t.Fatal("missing credential")
	}
	cfg.TokenEnv = "CLIENT"
	cfg.ManagerURL = "http://remote.example"
	if err := runMachineClient([]string{"--config", configPath(t, cfg)}, d); err == nil {
		t.Fatal("plaintext remote accepted")
	}
	cfg.ManagerURL = "http://127.0.0.1:1234"
	d.HTTPClient = &http.Client{Transport: serviceRoundTrip(func(r *http.Request) (*http.Response, error) { return nil, errors.New("fixture offline") })}
	if err := runMachineClient([]string{"--config", configPath(t, cfg)}, d); err == nil {
		t.Fatal("connection error hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Context = ctx
	if err := runMachineClient([]string{"--config", configPath(t, cfg)}, d); err != nil {
		t.Fatal("cancel not graceful", err)
	}
}

func TestManagerPackageStoreFailureReleasesJournal(t *testing.T) {
	d := serviceDeps()
	cfg := validManagerConfig(t)
	packagePath := filepath.Join(cfg.StateDir, "packages")
	if err := os.WriteFile(packagePath, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if server, store, err := buildManager(cfg, d); err == nil {
		server.Close()
		store.Close()
		t.Fatal("invalid package directory accepted")
	}
	if err := os.Remove(packagePath); err != nil {
		t.Fatal(err)
	}
	server, store, err := buildManager(cfg, d)
	if err != nil {
		t.Fatal("journal lock leaked after package initialization error", err)
	}
	server.Close()
	store.Close()
	server, store, err = buildManager(cfg, d)
	if err != nil {
		t.Fatal("package lock leaked after close", err)
	}
	server.Close()
	store.Close()
}

func TestManagerRejectsCorruptedClientInventory(t *testing.T) {
	cfg := validManagerConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "client-inventory.json"), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if server, store, err := buildManager(cfg, serviceDeps()); err == nil {
		server.Close()
		store.Close()
		t.Fatal("corrupted client inventory accepted")
	}
}

func TestManagerSetupUsageNamesSmidr(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	if !strings.Contains(output.String(), "manager-setup  explicitly register the remote Smidr and namespaced tools") || strings.Contains(output.String(), "CEO") {
		t.Fatal(output.String())
	}
}
