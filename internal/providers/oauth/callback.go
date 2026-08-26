package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/digitalygo/smidja/internal/authstore"
)

type callbackConfig struct {
	providerName         string
	path                 string
	expectedState        string
	requireState         bool
	checkStateBeforeCode bool
	deniedPageTitle      string
	deniedPageDetail     string
	deniedError          string
	missingCodeMessage   string
	successMessage       string
	exchange             func(context.Context, string) (authstore.Entry, error)
}

type callbackOutcome struct {
	code        string
	state       string
	entry       authstore.Entry
	hasEntry    bool
	err         error
	manualInput string
}

type callbackServer struct {
	cfg     callbackConfig
	ctx     context.Context
	url     string
	server  *http.Server
	mu      sync.Mutex
	claimed bool
	settled bool
	waitCh  chan callbackOutcome
}

func startCallbackServer(ctx context.Context, bindHost string, port int, redirectHost string, cfg callbackConfig) (*callbackServer, error) {
	server := &callbackServer{
		cfg:    cfg,
		ctx:    ctx,
		waitCh: make(chan callbackOutcome, 1),
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	tcpAddr := listener.Addr().(*net.TCPAddr)
	server.url = "http://" + net.JoinHostPort(redirectHost, strconv.Itoa(tcpAddr.Port)) + cfg.path
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handle)
	server.server = &http.Server{Handler: mux}
	go func() {
		err := server.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			server.settleError(err)
		}
	}()
	return server, nil
}

func (s *callbackServer) close() {
	s.server.Close()
}

func (s *callbackServer) wait(ctx context.Context) (callbackOutcome, error) {
	select {
	case outcome := <-s.waitCh:
		return outcome, nil
	case <-ctx.Done():
		return callbackOutcome{}, ctxErrMessage(ctx.Err())
	}
}

func (s *callbackServer) settle(outcome callbackOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return
	}
	s.settled = true
	s.waitCh <- outcome
}

func (s *callbackServer) settleError(err error) {
	s.settle(callbackOutcome{err: err})
}

func (s *callbackServer) settleEntry(entry authstore.Entry) {
	s.settle(callbackOutcome{entry: entry, hasEntry: true})
}

func (s *callbackServer) settleCapture(code, state string) {
	s.settle(callbackOutcome{code: code, state: state})
}

func (s *callbackServer) cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed || s.settled {
		return
	}
	s.settled = true
	s.waitCh <- callbackOutcome{}
}

func (s *callbackServer) claim() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed || s.settled {
		return false
	}
	s.claimed = true
	return true
}

func (s *callbackServer) handle(w http.ResponseWriter, r *http.Request) {
	requestURL, err := url.ParseRequestURI(r.RequestURI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal error while processing OAuth callback.", "")
		return
	}
	if r.Method != http.MethodGet || requestURL.Path != s.cfg.path {
		writeError(w, http.StatusNotFound, "OAuth callback route not found.", "")
		return
	}
	query := requestURL.Query()
	code := query.Get("code")
	state := query.Get("state")
	if s.cfg.deniedPageTitle != "" {
		if denied := query.Get("error"); denied != "" {
			description := query.Get("error_description")
			if description == "" {
				description = denied
			}
			writeError(w, http.StatusBadRequest, s.cfg.deniedPageTitle, fmt.Sprintf(s.cfg.deniedPageDetail, description))
			if s.cfg.deniedError != "" {
				s.settleError(fmt.Errorf(s.cfg.deniedError, description))
			}
			return
		}
	}
	if s.cfg.requireState && (code == "" || state == "") {
		writeError(w, http.StatusBadRequest, s.cfg.missingCodeMessage, "")
		return
	}
	if s.cfg.checkStateBeforeCode && s.cfg.expectedState != "" && state != s.cfg.expectedState {
		writeError(w, http.StatusBadRequest, "State mismatch.", "")
		return
	}
	if code == "" {
		writeError(w, http.StatusBadRequest, s.cfg.missingCodeMessage, "")
		return
	}
	if !s.cfg.checkStateBeforeCode && s.cfg.expectedState != "" && state != s.cfg.expectedState {
		writeError(w, http.StatusBadRequest, "State mismatch.", "")
		return
	}
	if !s.claim() {
		writeError(w, http.StatusConflict, "This OAuth callback has already been used.", "")
		return
	}
	if s.cfg.exchange != nil {
		entry, err := s.cfg.exchange(s.ctx, code)
		if err != nil {
			writeError(w, http.StatusBadGateway, s.cfg.providerName+" key exchange failed.", err.Error())
			s.settleError(err)
			return
		}
		writeSuccess(w, s.cfg.successMessage)
		s.settleEntry(entry)
		return
	}
	writeSuccess(w, s.cfg.successMessage)
	s.settleCapture(code, state)
}
