package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/gateway"
)

const (
	cookieName      = "smidja_session"
	csrfHeader      = "X-CSRF"
	transportWeb    = "web"
	webTokenEnv     = "SMIDJA_WEB_TOKEN"
	sessionLifetime = 24 * time.Hour
	maxSessions     = 1024
	maxLoginBytes   = 4 << 10
	maxSendBytes    = 1 << 20
	maxCancelBytes  = 4 << 10
	maxSendText     = 100_000
	defaultListen   = "127.0.0.1:8179"
)

type Gateway interface {
	Submit(ctx context.Context, msg gateway.InboundMessage) (gateway.Receipt, error)
	RegisterSink(transport string, sink gateway.DeliverySink)
	Cancel(transport, externalChatKey string) bool
}

type Config struct {
	ListenAddr       string
	AllowNonLoopback bool
	WebTokenFunc     func() (string, error)
	Gateway          Gateway
	Workspaces       map[string]string
}

type webSession struct {
	csrf    string
	expires time.Time
}

type sessionInfo struct {
	Key       string    `json:"-"`
	Workspace string    `json:"workspace"`
	CreatedAt time.Time `json:"createdAt"`
	SessionID string    `json:"sessionID,omitempty"`
}

type authCtx struct {
	userID string
	bearer bool
}

type Server struct {
	cfg     Config
	events  *eventRing
	mu      sync.Mutex
	cookies map[string]webSession
	known   map[string][]sessionInfo
}

func New(cfg Config) (*Server, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = defaultListen
	}
	if cfg.Gateway == nil {
		return nil, errors.New("web: gateway is required")
	}
	if len(cfg.Workspaces) == 0 {
		return nil, errors.New("web: at least one workspace is required")
	}
	for name, root := range cfg.Workspaces {
		if name == "" || root == "" {
			return nil, errors.New("web: workspace names and roots must not be empty")
		}
	}
	if !loopbackHost(cfg.ListenAddr) && !cfg.AllowNonLoopback {
		return nil, errors.New("web: binding a non-loopback address requires AllowNonLoopback")
	}
	if cfg.WebTokenFunc == nil {
		cfg.WebTokenFunc = func() (string, error) {
			token, ok := authstore.ResolveCredential("web", webTokenEnv, nil, os.Getenv)
			if !ok {
				return "", nil
			}
			return token, nil
		}
	}
	s := &Server{
		cfg:     cfg,
		events:  newEventRing(),
		cookies: make(map[string]webSession),
		known:   make(map[string][]sessionInfo),
	}
	cfg.Gateway.RegisterSink(transportWeb, s)
	return s, nil
}

func loopbackHost(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /api/csrf", s.withAuth(s.handleCSRF))
	mux.HandleFunc("GET /api/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("POST /api/send", s.withWriteAuth(s.handleSend))
	mux.HandleFunc("GET /api/events", s.withAuth(s.handleEvents))
	mux.HandleFunc("GET /api/transcript", s.withAuth(s.handleTranscript))
	mux.HandleFunc("POST /api/cancel", s.withWriteAuth(s.handleCancel))
	return mux
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, authCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		next(w, r, a)
	}
}

func (s *Server) withWriteAuth(next func(http.ResponseWriter, *http.Request, authCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		if !a.bearer {
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			if !s.validCSRF(a.userID, r.Header.Get(csrfHeader)) {
				writeError(w, http.StatusForbidden, "invalid csrf token")
				return
			}
		}
		next(w, r, a)
	}
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (authCtx, bool) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" && s.sessionValid(c.Value) {
		return authCtx{userID: c.Value}, true
	}
	if token := bearerToken(r); token != "" {
		want, err := s.cfg.WebTokenFunc()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "token resolution failed")
			return authCtx{}, false
		}
		if want != "" && tokenEqual(token, want) {
			sum := sha256.Sum256([]byte(token))
			return authCtx{userID: hex.EncodeToString(sum[:]), bearer: true}, true
		}
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
	return authCtx{}, false
}

func (s *Server) sessionValid(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.cookies[id]
	if !ok {
		return false
	}
	if time.Now().After(sess.expires) {
		delete(s.cookies, id)
		return false
	}
	return true
}

func (s *Server) validCSRF(userID, given string) bool {
	if given == "" {
		return false
	}
	s.mu.Lock()
	sess, ok := s.cookies[userID]
	s.mu.Unlock()
	return ok && tokenEqual(given, sess.csrf)
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func tokenEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func sameOrigin(r *http.Request) bool {
	ref := r.Header.Get("Origin")
	if ref == "" {
		ref = r.Header.Get("Referer")
	}
	if ref == "" {
		return true
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return false
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	if r.TLS != nil && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	body, err := readLimited(w, r, maxLoginBytes)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "invalid login body")
		return
	}
	want, err := s.cfg.WebTokenFunc()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token resolution failed")
		return
	}
	if want == "" {
		writeError(w, http.StatusServiceUnavailable, "web token not configured")
		return
	}
	if !tokenEqual(req.Token, want) {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	id := newID()
	csrf := newID()
	expires := time.Now().Add(sessionLifetime)
	s.mu.Lock()
	s.pruneLocked()
	s.cookies[id] = webSession{csrf: csrf, expires: expires}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		Secure:   r.TLS != nil,
	})
	writeJSON(w, http.StatusOK, map[string]string{"csrf": csrf})
}

func (s *Server) handleCSRF(w http.ResponseWriter, r *http.Request, a authCtx) {
	if a.bearer {
		writeJSON(w, http.StatusOK, map[string]string{"csrf": ""})
		return
	}
	s.mu.Lock()
	sess, ok := s.cookies[a.userID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"csrf": sess.csrf})
}

func (s *Server) pruneLocked() {
	now := time.Now()
	for id, sess := range s.cookies {
		if now.After(sess.expires) {
			delete(s.cookies, id)
		}
	}
	for id := range s.cookies {
		if len(s.cookies) <= maxSessions {
			return
		}
		delete(s.cookies, id)
	}
}

func newID() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("web: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

func newMessageID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("web: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

func readLimited(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func writeBodyError(w http.ResponseWriter, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid request body")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
