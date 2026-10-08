package sdk

type RenderContext struct {
	Width         int
	Streaming     bool
	Theme         Theme
	Cwd           string
	SessionID     string
	Model         string
	ThinkingLevel ThinkingLevel
}

type MessageRenderer func(ctx RenderContext, message CustomMessage) Component

type Entry struct {
	CustomType string
	Data       any
}

type EntryRenderer func(ctx RenderContext, entry Entry) Component

type MarkdownTransformContext struct {
	Kind      string
	Streaming bool
	Width     int
}

type MarkdownTransformer func(markdown string, ctx MarkdownTransformContext) string

type RendererRegistry interface {
	RegisterMessageRenderer(customType string, renderer MessageRenderer) error
	UnregisterMessageRenderer(customType string) error
	RegisterEntryRenderer(customType string, renderer EntryRenderer) error
	UnregisterEntryRenderer(customType string) error
	RegisterMarkdownTransformer(name string, transformer MarkdownTransformer) error
	UnregisterMarkdownTransformer(name string) error
}

type UIRegistrationAPI interface {
	RendererRegistry

	RegisterComponent(key string, factory ComponentFactory) error
	UnregisterComponent(key string) error
	RegisterWidget(key string, factory ComponentFactory) error
	UnregisterWidget(key string) error
	RegisterTerminalInputHook(key string, handler TerminalInputHandler) error
	UnregisterTerminalInputHook(key string) error
}
