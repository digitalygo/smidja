package ui

import (
	"context"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/sdk"
)

func invokeComponentFactory(factory sdk.ComponentFactory) (component sdk.Component, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("ui: extension component factory panic: %v", recovered)
		}
	}()
	component = factory()
	if component == nil {
		return nil, ErrExtensionUINilFactory
	}
	return component, nil
}

func (r *Runner) ShowModal(factory sdk.ModalFactory) (sdk.ModalResult, error) {
	return r.showModalWithContext(r.lifecycleCtx, factory)
}

func (r *Runner) showModalWithContext(ctx context.Context, factory sdk.ModalFactory) (sdk.ModalResult, error) {
	return r.showExtensionModal(ctx, func(done func(sdk.ModalResult)) (tui.Component, error) {
		if factory == nil {
			return nil, ErrExtensionUINilFactory
		}
		var (
			component sdk.Component
			err       error
		)
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("ui: custom modal factory panic: %v", recovered)
				}
			}()
			component = factory(done)
		}()
		if err != nil {
			return nil, err
		}
		if component == nil {
			return nil, ErrExtensionUINilFactory
		}
		return wrapExtensionComponentSync(component, r.reportExtensionPanic), nil
	})
}

func (r *Runner) ShowComponent(key string) (sdk.ModalResult, error) {
	return r.showComponentWithContext(r.lifecycleCtx, key)
}

func (r *Runner) showComponentWithContext(ctx context.Context, key string) (sdk.ModalResult, error) {
	registry := r.extensionRegistry()
	if registry == nil {
		return sdk.ModalResult{}, ErrExtensionUIUnavailable
	}
	if key == "" {
		return sdk.ModalResult{}, ErrExtensionUIKeyEmpty
	}
	factory, ok := registry.Component(key)
	if !ok {
		return sdk.ModalResult{}, fmt.Errorf("ui: extension component %q is not registered", key)
	}
	return r.showExtensionModal(ctx, func(done func(sdk.ModalResult)) (tui.Component, error) {
		raw, err := invokeComponentFactory(factory)
		if err != nil {
			return nil, err
		}
		if modalComponent, ok := raw.(sdk.ModalComponent); ok {
			if err := invokeEditorCallback(r.reportExtensionPanic, "modal-set-done", func() { modalComponent.SetModalDone(done) }); err != nil {
				disposeTUIComponent(raw)
				return nil, err
			}
		}
		return wrapExtensionComponentSync(raw, r.reportExtensionPanic), nil
	})
}

func (r *Runner) showExtensionModal(ctx context.Context, build func(done func(sdk.ModalResult)) (tui.Component, error)) (sdk.ModalResult, error) {
	if !r.Active() {
		return sdk.ModalResult{}, sdk.ErrModeUnsupported
	}
	if ctx == nil {
		ctx = r.dialogContext(nil)
	}
	results := make(chan sdk.ModalResult, 1)
	var once sync.Once
	done := func(value sdk.ModalResult) {
		once.Do(func() { results <- value })
	}
	component, err := build(done)
	if err != nil {
		return sdk.ModalResult{}, err
	}
	if component == nil {
		return sdk.ModalResult{}, errNilDialog
	}
	session, err := r.dialogs.acquire(ctx)
	if err != nil {
		disposeTUIComponent(component)
		return sdk.ModalResult{}, err
	}
	defer disposeTUIComponent(component)
	defer session.release()
	if _, outcome := r.dialogs.show(session, component); outcome != publishInstalled {
		return sdk.ModalResult{}, errDialogsClosed
	}
	select {
	case value := <-results:
		return value, nil
	case <-ctx.Done():
		done(sdk.ModalResult{Canceled: true})
		return <-results, nil
	case <-r.dialogs.done:
		done(sdk.ModalResult{Canceled: true})
		return <-results, nil
	}
}
