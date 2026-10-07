package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/sdk"
)

const promptCommandName = "prompt"

var promptShorthandReserved = map[string]struct{}{
	"agent":    {},
	"help":     {},
	"quit":     {},
	"exit":     {},
	"new":      {},
	"tree":     {},
	"fork":     {},
	"resume":   {},
	"sessions": {},
	"model":    {},
	"theme":    {},
	"settings": {},
}

func registerPromptCommand(commands *extensions.CommandCatalog, prompts content.PromptCatalog, output io.Writer, reserved map[string]struct{}) map[string]string {
	registerPromptHostCommand(commands, prompts, output)
	return registerPromptAliases(commands, prompts, reserved)
}

func registerPromptHostCommand(commands *extensions.CommandCatalog, prompts content.PromptCatalog, output io.Writer) string {
	if commands == nil {
		return ""
	}
	registered, err := commands.Register(promptCommandName, sdk.Command{
		Description: "run a prompt template; /prompt lists the available names",
		Handler: func(ctx sdk.CommandContext, args string) error {
			return handlePromptCommand(ctx, prompts, output, args)
		},
	})
	if err != nil {
		return ""
	}
	return registered
}

func registerPromptAliases(commands *extensions.CommandCatalog, prompts content.PromptCatalog, reserved map[string]struct{}) map[string]string {
	aliases := make(map[string]string)
	if commands == nil {
		return aliases
	}
	for _, entry := range prompts.Entries() {
		name := entry.Info.Name
		if _, taken := reserved[name]; taken {
			continue
		}
		if _, taken := commands.Get(name); taken {
			continue
		}
		description := entry.Info.Description
		if description == "" {
			description = "prompt template"
		}
		ref := entry.Ref
		commands.Register(name, sdk.Command{
			Description: description,
			Handler: func(ctx sdk.CommandContext, args string) error {
				return injectPrompt(ctx, ref, args)
			},
		})
		aliases[name] = name
	}
	return aliases
}

func handlePromptCommand(ctx sdk.CommandContext, prompts content.PromptCatalog, output io.Writer, args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		if output == nil {
			return nil
		}
		for _, name := range prompts.Names() {
			if _, err := fmt.Fprintln(output, sanitizeTerm(name)); err != nil {
				return err
			}
		}
		return nil
	}
	name, rest := splitFirstToken(trimmed)
	ref, ok := prompts.Lookup(name)
	if !ok {
		return fmt.Errorf("no prompt named %q", sanitizeTerm(name))
	}
	return injectPrompt(ctx, ref, rest)
}

func injectPrompt(ctx sdk.CommandContext, ref content.PromptRef, argumentText string) error {
	invocation, err := expandPromptReference(ref, argumentText)
	if err != nil {
		return err
	}
	injector, ok := ctx.(skillInjector)
	if !ok {
		return errors.New("prompt expansion is not available in this context")
	}
	return injector.runInput(invocation)
}

func expandPromptReference(ref content.PromptRef, argumentText string) (string, error) {
	args, err := content.ParsePromptArguments(argumentText)
	if err != nil {
		return "", fmt.Errorf("prompt %q: %w", sanitizeTerm(ref.Name), err)
	}
	expanded, err := content.ExpandPromptTemplate(ref.Content, args)
	if err != nil {
		return "", fmt.Errorf("prompt %q: %w", sanitizeTerm(ref.Name), err)
	}
	return expanded, nil
}

func splitFirstToken(input string) (first, rest string) {
	if i := strings.IndexAny(input, " \t\r\n"); i >= 0 {
		return input[:i], strings.TrimSpace(input[i+1:])
	}
	return input, ""
}

func (d *runDeps) expandPromptInput(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return input, nil
	}
	name, rest := splitCommandInput(trimmed)
	if name == d.explicitPromptCommand() {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "", errors.New("prompt: a template name is required")
		}
		templateName, argumentText := splitFirstToken(rest)
		ref, ok := d.prompts.Lookup(templateName)
		if !ok {
			return "", fmt.Errorf("no prompt named %q", sanitizeTerm(templateName))
		}
		return expandPromptReference(ref, argumentText)
	}
	if templateName, ok := d.promptAliases[name]; ok {
		ref, found := d.prompts.Lookup(templateName)
		if !found {
			return input, nil
		}
		return expandPromptReference(ref, rest)
	}
	return input, nil
}

func (d *runDeps) explicitPromptCommand() string {
	if d.promptCommand == "" {
		return promptCommandName
	}
	return d.promptCommand
}
