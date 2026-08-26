package sdk

type Extension interface {
	ID() string
}

type SetupHook interface {
	Setup(api API) error
}

type LLMHook interface {
	RegisterLLMHooks(r LLMHookRegistry)
}

type ToolHook interface {
	RegisterToolHooks(r ToolHookRegistry)
}

type SessionHook interface {
	RegisterSessionHooks(r SessionHookRegistry)
}
