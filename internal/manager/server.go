package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/clientpackages"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"github.com/digitalygo/smidja/internal/prreview"
)

type Remote interface {
	Capabilities(context.Context) (envai.Capabilities, error)
	ListTeams(context.Context) ([]envai.Team, error)
	CreateChat(context.Context, string, string) (envai.Chat, error)
	StartTurn(context.Context, string, string) (envai.Turn, error)
	FollowTurn(context.Context, string, string, int64, envai.EventHandler) (envai.TurnResult, error)
	CancelTurn(context.Context, string) error
}

type ToolBinding struct {
	Name     string `json:"name"`
	ClientID string `json:"client_id,omitempty"`
}

type Config struct {
	PRReview             *prreview.Service
	PackageRuntime       *clientpackages.Runtime
	LookupEnv            func(string) string
	ProtectedEnvironment []string
	Extensions           *extensiontools.Runner
	Private              *privateconfig.Store
	Packages             *managerpackages.Store
	Token                string
	WorkspaceID          string
	CEOTeamID            string
	CEOModel             string
	ClassifierModel      string
	SecureCookies        bool
	Remote               Remote
	Store                *managerstate.Store
	Tools                *machinetools.Service
	Clients              *machineclient.Broker
	ToolBindings         map[string]ToolBinding
	WorkflowRuntime      WorkflowRuntime
}

type loginSession struct {
	CSRF    string
	Expires time.Time
}
type Server struct {
	prReviewMu          sync.Mutex
	prReviewStatus      map[string]any
	cfg                 Config
	handler             http.Handler
	mu                  sync.Mutex
	capabilityMu        sync.Mutex
	packageToolRefs     map[string]map[string]string
	extensionToolRefs   map[string]string
	dynamicBindings     map[string]ToolBinding
	packageUpdateErrors map[string]string
	sessions            map[string]loginSession
	pending             map[string]func(context.Context)
	active              map[string]context.CancelFunc
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
}

func New(cfg Config) (*Server, error) {
	if len(cfg.Token) < 24 {
		return nil, errors.New("manager token must contain at least 24 characters")
	}
	if cfg.Store == nil {
		return nil, errors.New("manager store is required")
	}
	if cfg.Tools == nil {
		cfg.Tools = machinetools.New(machinetools.Config{})
	}
	if cfg.Clients == nil {
		cfg.Clients = machineclient.NewBroker(machineclient.BrokerConfig{})
	}
	if cfg.CEOModel == "" {
		cfg.CEOModel = "openai/gpt-6.1-terra"
	}
	if cfg.ClassifierModel == "" {
		cfg.ClassifierModel = "openai/gpt-6-luna-decisions"
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, dynamicBindings: map[string]ToolBinding{}, sessions: map[string]loginSession{}, pending: map[string]func(context.Context){}, active: map[string]context.CancelFunc{}, ctx: ctx, cancel: cancel}
	if s.cfg.WorkflowRuntime == nil {
		if remote, ok := cfg.Remote.(NativeRemote); ok {
			s.cfg.WorkflowRuntime = &nativeWorkflow{server: s, remote: remote}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("/api/clients/", cfg.Clients)
	mux.Handle("/api/", s.authorize(http.HandlerFunc(s.api)))
	s.handler = s.headers(mux)
	if cfg.Packages != nil {
		s.launch("packages:update", func(ctx context.Context) {
			cfg.Packages.RunUpdates(ctx, 0, func(results map[string]error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				s.packageUpdateErrors = map[string]string{}
				for id, err := range results {
					if err != nil {
						s.packageUpdateErrors[id] = err.Error()
					}
				}
			})
		})
	}
	for _, session := range cfg.Store.ListSessions() {
		s.observeSession(session.ID)
	}
	for _, task := range cfg.Store.ListTasks() {
		if task.Status == managerstate.Running {
			_, _ = cfg.Store.Pause(task.ID, "Manager restarted; remote phase outcome needs explicit recovery before resuming")
		} else if task.Status == managerstate.Queued {
			s.scheduleTask(task.ID)
		}
	}
	s.startPRReviewPolling()
	s.startClientExtensionSync()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }
func (s *Server) Close() {
	s.cancel()
	s.wg.Wait()
	_ = s.cfg.Clients.Close()
	if s.cfg.Packages != nil {
		_ = s.cfg.Packages.Close()
	}
	if s.cfg.Private != nil {
		_ = s.cfg.Private.Close()
	}
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
}

func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func sameSecret(a, b string) bool {
	aHash, bHash := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aHash[:], bHash[:]) == 1
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == r.Host && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, 403, errors.New("cross-origin request rejected"))
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := decode(w, r, &input, 4096); err != nil {
		return
	}
	if !sameSecret(input.Token, s.cfg.Token) {
		writeError(w, 401, errors.New("invalid manager token"))
		return
	}
	id, csrf := randomID(), randomID()
	s.mu.Lock()
	for key, v := range s.sessions {
		if time.Now().After(v.Expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 128 {
		s.mu.Unlock()
		writeError(w, 429, errors.New("too many sessions"))
		return
	}
	s.sessions[id] = loginSession{csrf, time.Now().Add(24 * time.Hour)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "smidja_manager", Value: id, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	writeJSON(w, 200, map[string]string{"csrf": csrf})
}
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, 403, errors.New("cross-origin request rejected"))
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Header.Get("Authorization") != "" && sameSecret(bearer, s.cfg.Token) {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("smidja_manager")
		if err != nil {
			writeError(w, 401, errors.New("sign in required"))
			return
		}
		s.mu.Lock()
		session, ok := s.sessions[cookie.Value]
		s.mu.Unlock()
		if !ok || time.Now().After(session.Expires) {
			writeError(w, 401, errors.New("session expired"))
			return
		}
		if r.Method != "GET" && !sameSecret(r.Header.Get("X-CSRF"), session.CSRF) {
			writeError(w, 403, errors.New("CSRF token required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeError(w, 400, fmt.Errorf("invalid request: %w", err))
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		e := errors.New("request must contain one JSON value")
		writeError(w, 400, e)
		return e
	}
	return nil
}
