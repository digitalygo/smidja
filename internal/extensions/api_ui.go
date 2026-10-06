package extensions

import (
	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/sdk"
)

var _ sdk.UIRegistrationAPI = (*api)(nil)

func (a *api) RegisterComponent(key string, factory sdk.ComponentFactory) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.RegisterComponent(key, factory)
}

func (a *api) UnregisterComponent(key string) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.UnregisterComponent(key)
}

func (a *api) RegisterWidget(key string, factory sdk.ComponentFactory) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.RegisterWidget(key, factory)
}

func (a *api) UnregisterWidget(key string) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.UnregisterWidget(key)
}

func (a *api) RegisterMessageRenderer(customType string, renderer sdk.MessageRenderer) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.RegisterMessageRenderer(customType, renderer)
}

func (a *api) UnregisterMessageRenderer(customType string) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.UnregisterMessageRenderer(customType)
}

func (a *api) RegisterEntryRenderer(customType string, renderer sdk.EntryRenderer) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.RegisterEntryRenderer(customType, renderer)
}

func (a *api) UnregisterEntryRenderer(customType string) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.UnregisterEntryRenderer(customType)
}

func (a *api) RegisterMarkdownTransformer(name string, transformer sdk.MarkdownTransformer) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.RegisterMarkdownTransformer(name, transformer)
}

func (a *api) UnregisterMarkdownTransformer(name string) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.UnregisterMarkdownTransformer(name)
}

func (a *api) RegisterTerminalInputHook(key string, handler sdk.TerminalInputHandler) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.RegisterTerminalInputHook(key, handler)
}

func (a *api) UnregisterTerminalInputHook(key string) error {
	if a.ui == nil {
		return sdk.ErrModeUnsupported
	}
	return a.ui.UnregisterTerminalInputHook(key)
}

func UIRegistryOf(value sdk.API) *extensionui.Registry {
	typed, ok := value.(*api)
	if !ok {
		return nil
	}
	return typed.ui
}
