package telegram

import "time"

const (
	TransportName            = "telegram"
	chatTypePrivate          = "private"
	actionTyping             = "typing"
	apiBaseDefault           = "https://api.telegram.org"
	defaultPollTimeoutSecs   = 50
	maxPollTimeoutSecs       = 50
	defaultBackoffBase       = time.Second
	defaultStreamingInterval = 3 * time.Second
	maxUpdates               = 100
	legacyChunkMax           = 4096
)

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID       int64  `json:"message_id"`
	MessageThreadID int64  `json:"message_thread_id,omitempty"`
	Chat            *Chat  `json:"chat,omitempty"`
	From            *User  `json:"from,omitempty"`
	Text            string `json:"text,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type ReplyParameters struct {
	MessageID int64 `json:"message_id"`
}

type InputRichMessage struct {
	Markdown string `json:"markdown,omitempty"`
}

type ResponseMessage struct {
	MessageID int64 `json:"message_id"`
}
