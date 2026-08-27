package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
)

const (
	StatusAccepted       = "accepted"
	StatusStarted        = "started"
	StatusCompleted      = "completed"
	StatusFailed         = "failed"
	StatusCancelled      = "cancelled"
	StatusOutcomeUnknown = "outcome_unknown"
)

var (
	ErrDuplicate         = errors.New("gateway: duplicate message id")
	ErrMailboxFull       = errors.New("gateway: actor mailbox full")
	ErrClosed            = errors.New("gateway: closed")
	ErrNotStarted        = errors.New("gateway: not started")
	ErrAlreadyStarted    = errors.New("gateway: already started")
	ErrRateLimited       = errors.New("gateway: rate limited")
	ErrTooManyActive     = errors.New("gateway: too many active turns")
	ErrInboundTooLarge   = errors.New("gateway: inbound message too large")
	ErrInvalidMessage    = errors.New("gateway: invalid inbound message")
	ErrTurnCancelled     = errors.New("gateway: turn cancelled")
	ErrRecordNotFound    = errors.New("gateway: journal record not found")
	ErrInvalidTransition = errors.New("gateway: invalid journal status transition")
)

type InboundMessage struct {
	ID              string
	Transport       string
	ExternalChatKey string
	UserIDHash      string
	Text            string
	SessionID       string
}

type Receipt struct {
	ID            string
	QueuePosition int
}

type WorkItem struct {
	SessionPath string
	Text        string
	EntriesDone func()
}

type RunResult struct {
	Text      string
	SessionID string
}

type Delivery struct {
	ID              string
	Transport       string
	ExternalChatKey string
	UserIDHash      string
	Text            string
	Result          RunResult
	Err             error
}

type TurnRunner interface {
	Run(ctx context.Context, work WorkItem) (RunResult, error)
}

type DeliverySink interface {
	Deliver(ctx context.Context, d Delivery) error
}

type Resolver func(key string) (workspaceRoot, sessionFileHint string)

func HashUserIdentity(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func RoutingKey(transport, externalChatKey string) string {
	return transport + ":" + externalChatKey
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	name := t.String()
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}
