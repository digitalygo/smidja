package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/digitalygo/smidja/internal/clientextensions"
	"github.com/digitalygo/smidja/internal/clientpackages"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/manager"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"github.com/digitalygo/smidja/internal/prreview"
)

type managerFileConfig struct {
	PRReview        *prreview.Config               `json:"pr_review,omitempty"`
	Listen          string                         `json:"listen"`
	StateDir        string                         `json:"state_dir"`
	TokenEnv        string                         `json:"token_env"`
	BehindTLSProxy  bool                           `json:"behind_tls_proxy"`
	EnvaiURL        string                         `json:"envai_url"`
	EnvaiKeyEnv     string                         `json:"envai_key_env"`
	WorkspaceID     string                         `json:"workspace_id"`
	CEOTeamID       string                         `json:"ceo_team_id"`
	CEOModel        string                         `json:"ceo_model"`
	ClassifierModel string                         `json:"classifier_model"`
	ToolBindings    map[string]manager.ToolBinding `json:"tool_bindings"`
	Clients         []clientIdentity               `json:"clients"`
}

type clientIdentity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TokenEnv string `json:"token_env"`
}

type clientFileConfig struct {
	StateDir                     string                   `json:"state_dir,omitempty"`
	PackageSources               []managerpackages.Source `json:"package_sources,omitempty"`
	PackageUpdateIntervalSeconds int                      `json:"package_update_interval_seconds,omitempty"`
	ManagerURL                   string                   `json:"manager_url"`
	ClientID                     string                   `json:"client_id"`
	Name                         string                   `json:"name"`
	TokenEnv                     string                   `json:"token_env"`
}

func readServiceConfig(args []string, name string, d *Deps, value any) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	path := fs.String("config", "", "path to JSON configuration (credentials are environment references)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return fmt.Errorf("usage: smidja %s --config path.json", name)
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid %s configuration: %w", name, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return errors.New("configuration must contain one JSON value")
	}
	return nil
}

func referencedSecret(d *Deps, key, label string, min int) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%s environment reference is required", label)
	}
	value := d.Env(key)
	if len(value) < min || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%s environment variable %s is missing or invalid", label, key)
	}
	return value, nil
}

func serviceEnvironment(d *Deps) []string {
	result := []string{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TMPDIR", "TMP", "TEMP", "TERM"} {
		if value := d.Env(key); value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}

func validateManagerListen(cfg *managerFileConfig) error {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8787"
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("invalid manager listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) && !cfg.BehindTLSProxy {
		return errors.New("non-loopback bind requires behind_tls_proxy and a configured HTTPS reverse proxy")
	}
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return errors.New("state_dir must be an absolute path")
	}
	return nil
}

func buildManager(cfg managerFileConfig, d *Deps) (*manager.Server, *managerstate.Store, error) {
	if err := validateManagerListen(&cfg); err != nil {
		return nil, nil, err
	}
	token, err := referencedSecret(d, cfg.TokenEnv, "manager token", 24)
	if err != nil {
		return nil, nil, err
	}
	key, err := referencedSecret(d, cfg.EnvaiKeyEnv, "Envai API key", 1)
	if err != nil {
		return nil, nil, err
	}
	if cfg.WorkspaceID == "" || cfg.CEOTeamID == "" {
		return nil, nil, errors.New("workspace_id and ceo_team_id are required")
	}
	remote, err := envai.New(envai.Config{BaseURL: cfg.EnvaiURL, APIKey: key, HTTPClient: d.HTTPClient})
	if err != nil {
		return nil, nil, err
	}
	var review *prreview.Service
	if cfg.PRReview != nil {
		review, err = prreview.New(*cfg.PRReview, d.HTTPClient, d.Env)
		if err != nil {
			return nil, nil, err
		}
	}
	broker := machineclient.NewBroker(machineclient.BrokerConfig{InventoryPath: filepath.Join(cfg.StateDir, "client-inventory.json"), CredentialsPath: filepath.Join(cfg.StateDir, "client-credentials.jsonl")})
	keepBroker := false
	defer func() {
		if !keepBroker {
			broker.Close()
		}
	}()
	if err = broker.Err(); err != nil {
		return nil, nil, err
	}
	for _, client := range cfg.Clients {
		secret, err := referencedSecret(d, client.TokenEnv, "client token", 16)
		if err != nil {
			return nil, nil, err
		}
		if secret == token {
			return nil, nil, errors.New("client token must differ from manager token")
		}
		if err = broker.AddClient(client.ID, client.Name, secret); err != nil {
			return nil, nil, err
		}
	}
	store, err := managerstate.Open(cfg.StateDir)
	if err != nil {
		return nil, nil, err
	}
	packageVersion := strings.TrimPrefix(Version, "v")
	if packageVersion == "dev" {
		packageVersion = "0.0.0"
	}
	packageStore, err := managerpackages.Open(managerpackages.Config{Dir: filepath.Join(cfg.StateDir, "packages"), Version: packageVersion, Target: runtime.GOOS + "/" + runtime.GOARCH, Scope: "manager", HTTPClient: d.HTTPClient, LookupEnv: d.Env})
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	privateStore, err := privateconfig.Open(filepath.Join(cfg.StateDir, "private"))
	if err != nil {
		packageStore.Close()
		store.Close()
		return nil, nil, err
	}
	_, err = privateStore.PurgeExpired(time.Now())
	if err != nil {
		privateStore.Close()
		packageStore.Close()
		store.Close()
		return nil, nil, err
	}
	protectedEnvironment := []string{cfg.TokenEnv, cfg.EnvaiKeyEnv, "SMIDJA_WEBHOOK_SECRET"}
	if cfg.PRReview != nil && cfg.PRReview.TokenEnv != "" {
		protectedEnvironment = append(protectedEnvironment, cfg.PRReview.TokenEnv)
	}
	for _, client := range cfg.Clients {
		protectedEnvironment = append(protectedEnvironment, client.TokenEnv)
	}
	packageRuntime, err := clientpackages.New(clientpackages.Config{Store: packageStore, Environment: serviceEnvironment(d), LookupEnv: d.Env, ProtectedEnvironment: protectedEnvironment})
	if err != nil {
		privateStore.Close()
		packageStore.Close()
		store.Close()
		return nil, nil, err
	}
	server, err := manager.New(manager.Config{PRReview: review, PackageRuntime: packageRuntime, LookupEnv: d.Env, ProtectedEnvironment: protectedEnvironment, Extensions: extensiontools.New(nil, serviceEnvironment(d)), Private: privateStore, Packages: packageStore, Token: token, WorkspaceID: cfg.WorkspaceID, CEOTeamID: cfg.CEOTeamID, CEOModel: cfg.CEOModel, ClassifierModel: cfg.ClassifierModel, SecureCookies: cfg.BehindTLSProxy, Remote: remote, Store: store, Tools: machinetools.New(machinetools.Config{Environment: serviceEnvironment(d)}), Clients: broker, ToolBindings: cfg.ToolBindings})
	if err != nil {
		privateStore.Close()
		packageStore.Close()
		store.Close()
		return nil, nil, err
	}
	keepBroker = true
	return server, store, nil
}

func runManager(args []string, d *Deps) error {
	var cfg managerFileConfig
	if err := readServiceConfig(args, "manager", d, &cfg); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fail(d, err)
	}
	if err := validateManagerListen(&cfg); err != nil {
		return fail(d, err)
	}
	server, store, err := buildManager(cfg, d)
	if err != nil {
		return fail(d, err)
	}
	defer store.Close()
	defer server.Close()
	ctx, stop := signal.NotifyContext(d.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fail(d, err)
	}
	fmt.Fprintf(d.Stdout, "Smidja manager listening on %s\n", listener.Addr())
	return serveManager(ctx, listener, server)
}

func serveManager(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10}
	completed := make(chan error, 1)
	go func() { completed <- server.Serve(listener) }()
	select {
	case err := <-completed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
			return err
		}
		err := <-completed
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func runMachineClient(args []string, d *Deps) error {
	var cfg clientFileConfig
	if err := readServiceConfig(args, "client", d, &cfg); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fail(d, err)
	}
	token, err := referencedSecret(d, cfg.TokenEnv, "client token", 16)
	if err != nil {
		return fail(d, err)
	}
	inventory := machineclient.Inventory{Tools: []string{}}
	for _, tool := range machinetools.Definitions() {
		inventory.Tools = append(inventory.Tools, tool.Name)
	}
	ctx, stop := signal.NotifyContext(d.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()
	base := machinetools.New(machinetools.Config{Environment: serviceEnvironment(d)})
	var executor machineclient.Executor = base
	clientConfig := machineclient.Config{ManagerURL: cfg.ManagerURL, ClientID: cfg.ClientID, Name: cfg.Name, Token: token, HTTPClient: d.HTTPClient, Inventory: inventory}
	if cfg.StateDir == "" && (len(cfg.PackageSources) > 0 || cfg.PackageUpdateIntervalSeconds != 0) {
		return fail(d, errors.New("client package configuration requires state_dir"))
	}
	if cfg.PackageUpdateIntervalSeconds < 0 {
		return fail(d, errors.New("package update interval cannot be negative"))
	}
	if cfg.StateDir != "" {
		version := strings.TrimPrefix(Version, "v")
		if version == "dev" {
			version = "0.0.0"
		}
		store, err := managerpackages.Open(managerpackages.Config{Dir: filepath.Join(cfg.StateDir, "packages"), Version: version, Target: runtime.GOOS + "/" + runtime.GOARCH, Scope: "client", HTTPClient: d.HTTPClient, LookupEnv: d.Env})
		if err != nil {
			return fail(d, err)
		}
		defer store.Close()
		protected := []string{cfg.TokenEnv}
		for _, source := range cfg.PackageSources {
			if source.TokenEnv == cfg.TokenEnv {
				return fail(d, errors.New("package source cannot reuse the client authentication token"))
			}
			if source.TokenEnv != "" {
				protected = append(protected, source.TokenEnv)
			}
			installed := false
			for _, revision := range store.List() {
				if revision.Source == source {
					installed = true
					break
				}
			}
			if !installed {
				if _, err = store.Install(ctx, source); err != nil {
					return fail(d, err)
				}
			}
		}
		packages, err := clientpackages.New(clientpackages.Config{Store: store, Base: base, Environment: serviceEnvironment(d), LookupEnv: d.Env, ProtectedEnvironment: protected})
		if err != nil {
			return fail(d, err)
		}
		extensions, err := clientextensions.Open(clientextensions.Config{Dir: filepath.Join(cfg.StateDir, "extensions"), ClientID: cfg.ClientID, Base: packages, InventoryProvider: packages.Inventory, Environment: serviceEnvironment(d), LookupEnv: d.Env, ProtectedEnvironment: protected})
		if err != nil {
			return fail(d, err)
		}
		defer extensions.Close()
		executor = extensions
		clientConfig.InventoryProvider = extensions.Inventory
		updateCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			store.RunUpdates(updateCtx, time.Duration(cfg.PackageUpdateIntervalSeconds)*time.Second, func(results map[string]error) {
				for id, err := range results {
					if err != nil {
						fmt.Fprintf(d.Stderr, "smidja: package %s update: %v\n", id, err)
					}
				}
			})
		}()
		defer func() { cancel(); <-done }()
	}
	err = machineclient.Run(ctx, clientConfig, executor)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fail(d, err)
	}
	return nil
}
