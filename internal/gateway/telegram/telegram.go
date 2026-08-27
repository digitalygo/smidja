package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/gateway"
)

type Gateway interface {
	Submit(ctx context.Context, msg gateway.InboundMessage) (gateway.Receipt, error)
	RegisterSink(transport string, sink gateway.DeliverySink)
}

type Options struct {
	Gateway           Gateway
	Token             func() (string, error)
	AllowedUserIDs    []int64
	WorkspaceForChat  func(chatKey string) (string, string)
	PollTimeoutSecs   int
	APIBase           string
	HTTP              *http.Client
	Streaming         bool
	StreamingInterval time.Duration
	BackoffBase       time.Duration
	Sleep             func(ctx context.Context, d time.Duration) error
}

type Telegram struct {
	opts    Options
	allowed map[int64]struct{}
	client  atomic.Pointer[client]
	started atomic.Bool
}

func New(opts Options) *Telegram {
	t := &Telegram{opts: opts}
	if len(opts.AllowedUserIDs) > 0 {
		t.allowed = make(map[int64]struct{}, len(opts.AllowedUserIDs))
		for _, id := range opts.AllowedUserIDs {
			t.allowed[id] = struct{}{}
		}
	}
	if opts.Gateway != nil {
		opts.Gateway.RegisterSink(TransportName, t)
	}
	return t
}

func TokenFromAuth(store *authstore.Store, env func(string) string) func() (string, error) {
	return func() (string, error) {
		token, ok := authstore.ResolveCredential("telegram", "TELEGRAM_BOT_TOKEN", store, env)
		if !ok {
			return "", errors.New("telegram: bot token not found in TELEGRAM_BOT_TOKEN or authstore provider telegram")
		}
		return token, nil
	}
}

func (t *Telegram) Start(ctx context.Context) error {
	if !t.started.CompareAndSwap(false, true) {
		return errors.New("telegram: already started")
	}
	defer t.started.Store(false)
	if t.opts.Gateway == nil {
		return errors.New("telegram: missing gateway")
	}
	if t.opts.Token == nil {
		return errors.New("telegram: missing token resolver")
	}
	token, err := t.opts.Token()
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("telegram: empty bot token")
	}
	c := &client{base: t.apiBase(), token: token, http: t.httpClient()}
	t.client.Store(c)
	t.opts.Gateway.RegisterSink(TransportName, t)
	defer t.opts.Gateway.RegisterSink(TransportName, nil)
	if _, err := c.getMe(ctx); err != nil {
		return err
	}
	if err := c.deleteWebhook(ctx); err != nil {
		return err
	}
	return t.pollLoop(ctx, c)
}

func (t *Telegram) pollLoop(ctx context.Context, c *client) error {
	var offset int64
	backoff := t.backoffBase()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		updates, err := c.getUpdates(ctx, offset, t.pollTimeoutSecs())
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			delay := t.retryDelay(err, backoff)
			if sleepErr := t.sleep(ctx, delay); sleepErr != nil {
				return nil
			}
			backoff = min(backoff*2, t.backoffCap())
			continue
		}
		backoff = t.backoffBase()
		for i := range updates {
			t.handleUpdate(c, updates[i])
		}
		if n := len(updates); n > 0 {
			offset = updates[n-1].UpdateID + 1
		}
	}
}

func (t *Telegram) handleUpdate(c *client, u Update) {
	if u.Message == nil || u.Message.Chat == nil || u.Message.From == nil {
		return
	}
	m := u.Message
	if m.Chat.Type != chatTypePrivate || m.Text == "" {
		return
	}
	if !t.isAllowed(m.From.ID) {
		return
	}
	inbound := gateway.InboundMessage{
		ID:              fmt.Sprintf("%d:%d", u.UpdateID, m.MessageID),
		Transport:       TransportName,
		ExternalChatKey: chatKey(m.Chat.ID, m.MessageThreadID, m.From.ID),
		UserIDHash:      gateway.HashUserIdentity(strconv.FormatInt(m.From.ID, 10)),
		Text:            m.Text,
	}
	if _, err := t.opts.Gateway.Submit(context.Background(), inbound); err == nil {
		_ = c.sendChatAction(context.Background(), m.Chat.ID, m.MessageThreadID, actionTyping)
	}
}

func (t *Telegram) Resolver(key string) (string, string) {
	if t.opts.WorkspaceForChat == nil {
		return "", ""
	}
	return t.opts.WorkspaceForChat(strings.TrimPrefix(key, TransportName+":"))
}

func (t *Telegram) isAllowed(userID int64) bool {
	if len(t.allowed) == 0 {
		return false
	}
	_, ok := t.allowed[userID]
	return ok
}

func chatKey(chatID, threadID, userID int64) string {
	return TransportName + ":" + strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(threadID, 10) + ":" + strconv.FormatInt(userID, 10)
}

func parseChatKey(key string) (chatID, threadID, userID int64, err error) {
	parts := strings.Split(key, ":")
	if len(parts) != 4 || parts[0] != TransportName {
		return 0, 0, 0, errors.New("telegram: invalid chat key")
	}
	chatID, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, 0, errors.New("telegram: invalid chat id in chat key")
	}
	threadID, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, 0, 0, errors.New("telegram: invalid thread id in chat key")
	}
	userID, err = strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return 0, 0, 0, errors.New("telegram: invalid user id in chat key")
	}
	return chatID, threadID, userID, nil
}

func (t *Telegram) retryDelay(err error, backoff time.Duration) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return time.Duration(apiErr.RetryAfter) * time.Second
	}
	return backoff
}

func (t *Telegram) pollTimeoutSecs() int {
	secs := t.opts.PollTimeoutSecs
	if secs <= 0 {
		secs = defaultPollTimeoutSecs
	}
	if secs > maxPollTimeoutSecs {
		secs = maxPollTimeoutSecs
	}
	return secs
}

func (t *Telegram) backoffBase() time.Duration {
	if t.opts.BackoffBase > 0 {
		return t.opts.BackoffBase
	}
	return defaultBackoffBase
}

func (t *Telegram) backoffCap() time.Duration {
	return time.Duration(t.pollTimeoutSecs())*time.Second + 30*time.Second
}

func (t *Telegram) sleep(ctx context.Context, d time.Duration) error {
	if t.opts.Sleep != nil {
		return t.opts.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (t *Telegram) apiBase() string {
	if t.opts.APIBase != "" {
		return strings.TrimSuffix(t.opts.APIBase, "/")
	}
	return apiBaseDefault
}

func (t *Telegram) httpClient() *http.Client {
	if t.opts.HTTP != nil {
		return t.opts.HTTP
	}
	return http.DefaultClient
}
