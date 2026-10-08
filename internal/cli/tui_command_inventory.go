package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type tuiCommandDescriptor struct {
	Name        string
	Description string
	Handler     func(b *tuiBridge, args string) error
}

func (b *tuiBridge) hostCommandDescriptors() []tuiCommandDescriptor {
	return []tuiCommandDescriptor{
		{Name: "new", Description: "start a new session", Handler: func(bridge *tuiBridge, args string) error {
			return bridge.newSession(args)
		}},
		{Name: "tree", Description: "browse the current session tree", Handler: func(bridge *tuiBridge, args string) error {
			return bridge.showTree()
		}},
		{Name: "fork", Description: "fork the current session at an entry", Handler: func(bridge *tuiBridge, args string) error {
			return bridge.forkSession(args)
		}},
		{Name: "resume", Description: "resume a session by path or id", Handler: func(bridge *tuiBridge, args string) error {
			return bridge.resumeSession(args)
		}},
		{Name: "sessions", Description: "browse sessions to resume, rename, or delete", Handler: func(bridge *tuiBridge, args string) error {
			return bridge.sessionsBrowser()
		}},
		{Name: "help", Description: "show command help", Handler: func(bridge *tuiBridge, args string) error {
			bridge.showHelp()
			return nil
		}},
		{Name: "model", Description: "select the model for the next turn", Handler: func(bridge *tuiBridge, args string) error {
			bridge.selectModel()
			return nil
		}},
		{Name: "theme", Description: "select and apply a theme", Handler: func(bridge *tuiBridge, args string) error {
			bridge.selectTheme()
			return nil
		}},
		{Name: "settings", Description: "change session settings", Handler: func(bridge *tuiBridge, args string) error {
			bridge.showSettings()
			return nil
		}},
		{Name: "quit", Description: "end the session", Handler: func(bridge *tuiBridge, args string) error {
			bridge.runner.RequestExit()
			return nil
		}},
		{Name: "exit", Description: "end the session", Handler: func(bridge *tuiBridge, args string) error {
			bridge.runner.RequestExit()
			return nil
		}},
	}
}

func collisionAlias(base string, taken map[string]struct{}) string {
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

func (b *tuiBridge) commandInventory() []tuiCommandDescriptor {
	host := b.hostCommandDescriptors()
	taken := make(map[string]struct{}, len(host))
	inventory := make([]tuiCommandDescriptor, 0, len(host))
	for _, descriptor := range host {
		taken[descriptor.Name] = struct{}{}
		inventory = append(inventory, descriptor)
	}
	if b.rd.commands == nil {
		return inventory
	}
	for _, command := range b.rd.commands.List() {
		name := command.Name
		if _, collision := taken[name]; collision {
			alias := collisionAlias(name, taken)
			taken[alias] = struct{}{}
			original := command.Name
			description := command.Description
			inventory = append(inventory, tuiCommandDescriptor{
				Name:        alias,
				Description: description,
				Handler: func(bridge *tuiBridge, args string) error {
					return bridge.runExtensionCommand(original, args)
				},
			})
			continue
		}
		taken[name] = struct{}{}
		cmd := command
		inventory = append(inventory, tuiCommandDescriptor{
			Name:        name,
			Description: cmd.Description,
			Handler: func(bridge *tuiBridge, args string) error {
				return bridge.runExtensionCommand(cmd.Name, args)
			},
		})
	}
	return inventory
}

func (b *tuiBridge) runExtensionCommand(name, args string) error {
	cmd, ok := b.rd.commands.Get(name)
	if !ok {
		return nil
	}
	hctx := b.rd.handlerContext(b.ctx)
	invocation := newCommandInvocation(handlerSignal(hctx, b.ctx))
	defer invocation.invalidate()
	commandCtx := &tuiCommandContext{HandlerContext: hctx, bridge: b, invocation: invocation}
	b.capture.Begin()
	err := cmd.Handler(commandCtx, args)
	captured := b.capture.End()
	if captured != "" {
		b.runner.Surface().AddNotice(interactive.NoticeInfo, captured)
	}
	b.syncCommandInventory()
	return err
}

func handlerSignal(hctx sdk.HandlerContext, fallback context.Context) context.Context {
	if hctx != nil {
		if signal := hctx.Signal(); signal != nil {
			return signal
		}
	}
	return fallback
}

func (b *tuiBridge) dispatchCommand(name, args string) bool {
	for _, descriptor := range b.commandInventory() {
		if descriptor.Name != name {
			continue
		}
		if err := descriptor.Handler(b, args); err != nil {
			b.runner.Surface().AddNotice(interactive.NoticeWarning, "/"+name+": "+err.Error())
		}
		return true
	}
	return false
}

func (b *tuiBridge) effectiveHelpEntries() []ui.HelpEntry {
	descriptors := b.commandInventory()
	entries := make([]ui.HelpEntry, 0, len(descriptors))
	for _, descriptor := range descriptors {
		entries = append(entries, ui.HelpEntry{Name: descriptor.Name, Description: descriptor.Description})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func (b *tuiBridge) autocompleteInventory() []tui.AutocompleteItem {
	descriptors := b.commandInventory()
	items := make([]tui.AutocompleteItem, 0, len(descriptors))
	for _, descriptor := range descriptors {
		items = append(items, tui.AutocompleteItem{
			Value:       descriptor.Name,
			Label:       "/" + descriptor.Name,
			Description: descriptor.Description,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Value < items[j].Value })
	return items
}

func (b *tuiBridge) syncCommandInventory() {
	if b.runner == nil {
		return
	}
	surface := b.runner.Surface()
	if surface == nil || surface.Editor() == nil {
		return
	}
	surface.Editor().SetCommandInventory(b.autocompleteInventory())
}

func (b *tuiBridge) commandNames() []string {
	descriptors := b.commandInventory()
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		names = append(names, descriptor.Name)
	}
	return names
}

func (b *tuiBridge) hasCommand(name string) bool {
	for _, descriptor := range b.commandInventory() {
		if descriptor.Name == name {
			return true
		}
	}
	return false
}
