package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/sdk"
)

func promptCatalog(prompts map[string]string) content.PromptCatalog {
	snapshot := content.Snapshot{Prompts: make(map[string]content.PromptRef, len(prompts))}
	for name, body := range prompts {
		snapshot.Prompts[name] = content.PromptRef{Name: name, Content: body}
	}
	return content.NewPromptCatalog(snapshot)
}

type captureInjectorContext struct {
	fakeCommandContext
	input string
}

func (c *captureInjectorContext) runInput(input string) error {
	c.input = input
	return nil
}

func TestPromptCommandListsNames(t *testing.T) {
	cat := promptCatalog(map[string]string{"beta": "b", "alpha": "a"})
	commands := extensions.NewCommandCatalog()
	var out bytes.Buffer
	registerPromptCommand(commands, cat, &out, promptShorthandReserved)
	cmd, ok := commands.Get("prompt")
	if !ok {
		t.Fatal("prompt command missing")
	}
	if err := cmd.Handler(&fakeCommandContext{}, ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := out.String(); got != "alpha\nbeta\n" {
		t.Fatalf("list output = %q, want sorted names", got)
	}
	if err := cmd.Handler(&fakeCommandContext{}, "   "); err != nil {
		t.Fatalf("blank list: %v", err)
	}
	if got := out.String(); got != "alpha\nbeta\nalpha\nbeta\n" {
		t.Fatalf("blank list output = %q", got)
	}
}

func TestPromptCommandExpandsAndInjects(t *testing.T) {
	cat := promptCatalog(map[string]string{"greet": "hello $1 from $ARGUMENTS"})
	commands := extensions.NewCommandCatalog()
	registerPromptCommand(commands, cat, &bytes.Buffer{}, promptShorthandReserved)
	cmd, ok := commands.Get("prompt")
	if !ok {
		t.Fatal("prompt command missing")
	}
	ctx := &captureInjectorContext{}
	if err := cmd.Handler(ctx, `greet "Bob Smith" extra`); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if want := "hello Bob Smith from Bob Smith extra"; ctx.input != want {
		t.Fatalf("injected input = %q, want %q", ctx.input, want)
	}
}

func TestPromptCommandErrors(t *testing.T) {
	cat := promptCatalog(map[string]string{"greet": "hello $1"})
	commands := extensions.NewCommandCatalog()
	registerPromptCommand(commands, cat, &bytes.Buffer{}, promptShorthandReserved)
	cmd, _ := commands.Get("prompt")

	ctx := &captureInjectorContext{}
	err := cmd.Handler(ctx, "missing value")
	if err == nil || !strings.Contains(err.Error(), "no prompt named") {
		t.Fatalf("unknown prompt error = %v", err)
	}
	if ctx.input != "" {
		t.Fatalf("unknown prompt injected %q", ctx.input)
	}

	err = cmd.Handler(&captureInjectorContext{}, `greet "unclosed`)
	if err == nil || !strings.Contains(err.Error(), "unmatched double quote") {
		t.Fatalf("unmatched quote error = %v", err)
	}
	if !strings.Contains(err.Error(), `prompt "greet"`) {
		t.Fatalf("unmatched quote error lacks the prompt name: %v", err)
	}

	err = cmd.Handler(&fakeCommandContext{}, "greet bob")
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("non-injector error = %v", err)
	}

	if err := handlePromptCommand(&captureInjectorContext{}, content.NewPromptCatalog(content.Snapshot{}), nil, ""); err != nil {
		t.Fatalf("nil output listing: %v", err)
	}
}

func TestPromptShorthandCollisions(t *testing.T) {
	cat := promptCatalog(map[string]string{
		"free":  "free $1",
		"help":  "help body",
		"model": "model body",
		"skill": "skill body",
		"blank": "   ",
	})
	commands := extensions.NewCommandCatalog()
	if _, err := commands.Register("skill", sdk.Command{Description: "original skill"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	aliases := registerPromptCommand(commands, cat, &out, promptShorthandReserved)

	free, ok := commands.Get("free")
	if !ok {
		t.Fatal("free shorthand missing")
	}
	ctx := &captureInjectorContext{}
	if err := free.Handler(ctx, "tail"); err != nil {
		t.Fatalf("shorthand: %v", err)
	}
	if ctx.input != "free tail" {
		t.Fatalf("shorthand injected %q", ctx.input)
	}
	if skill, _ := commands.Get("skill"); skill.Description != "original skill" {
		t.Fatalf("skill command overwritten: %+v", skill)
	}
	if _, ok := commands.Get("help"); ok {
		t.Fatal("help must stay reserved")
	}
	if _, ok := commands.Get("model"); ok {
		t.Fatal("model must stay reserved")
	}
	if _, ok := aliases["help"]; ok {
		t.Fatal("aliases must not contain reserved names")
	}
	if _, ok := aliases["skill"]; ok {
		t.Fatal("aliases must not contain catalog collisions")
	}
	if aliases["free"] != "free" {
		t.Fatalf("aliases = %#v", aliases)
	}

	blank, ok := commands.Get("blank")
	if !ok {
		t.Fatal("blank shorthand missing")
	}
	if blank.Description != "prompt template" {
		t.Fatalf("blank description = %q", blank.Description)
	}
	blankCtx := &captureInjectorContext{}
	if err := blank.Handler(blankCtx, "ignored"); err != nil {
		t.Fatalf("blank shorthand: %v", err)
	}
	if blankCtx.input != "   " {
		t.Fatalf("blank shorthand injected %q", blankCtx.input)
	}

	prompt, _ := commands.Get("prompt")
	explicit := &captureInjectorContext{}
	if err := prompt.Handler(explicit, "help"); err != nil {
		t.Fatalf("explicit /prompt help: %v", err)
	}
	if explicit.input != "help body" {
		t.Fatalf("explicit resolution injected %q", explicit.input)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestPromptCommandListingWriteFailure(t *testing.T) {
	cat := promptCatalog(map[string]string{"a": "body"})
	err := handlePromptCommand(&fakeCommandContext{}, cat, failingWriter{}, "")
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("listing error = %v", err)
	}
}

func TestPromptShorthandReservedMatchesHostCommands(t *testing.T) {
	host := make(map[string]struct{})
	for _, descriptor := range (&tuiBridge{}).hostCommandDescriptors() {
		host[descriptor.Name] = struct{}{}
		if _, ok := promptShorthandReserved[descriptor.Name]; !ok {
			t.Fatalf("host command %q missing from the reserved set", descriptor.Name)
		}
	}
	for name := range promptShorthandReserved {
		switch name {
		case "agent", "help", "quit", "exit":
			continue
		}
		if _, ok := host[name]; !ok {
			t.Fatalf("reserved name %q matches no host command", name)
		}
	}
}

func TestPromptAgentShorthandReservedForFutureBuiltin(t *testing.T) {
	cat := promptCatalog(map[string]string{"agent": "agent body $1"})
	commands := extensions.NewCommandCatalog()
	aliases := registerPromptCommand(commands, cat, &bytes.Buffer{}, promptShorthandReserved)
	if _, ok := commands.Get("agent"); ok {
		t.Fatal("/agent must stay reserved for the future builtin shorthand")
	}
	if _, ok := aliases["agent"]; ok {
		t.Fatal("agent alias must not be registered")
	}
	prompt, ok := commands.Get("prompt")
	if !ok {
		t.Fatal("prompt command missing")
	}
	ctx := &captureInjectorContext{}
	if err := prompt.Handler(ctx, "agent tail"); err != nil {
		t.Fatalf("explicit /prompt agent: %v", err)
	}
	if ctx.input != "agent body tail" {
		t.Fatalf("explicit resolution injected %q", ctx.input)
	}
	d := &runDeps{prompts: cat, promptAliases: aliases, promptCommand: promptCommandName}
	got, err := d.expandPromptInput("/agent tail")
	if err != nil {
		t.Fatalf("reserved /agent input: %v", err)
	}
	if got != "/agent tail" {
		t.Fatalf("reserved /agent expanded to %q, want the raw input", got)
	}
	got, err = d.expandPromptInput("/prompt agent tail")
	if err != nil {
		t.Fatalf("explicit /prompt agent: %v", err)
	}
	if got != "agent body tail" {
		t.Fatalf("explicit /prompt agent expanded to %q", got)
	}
}

func TestPromptHostCommandPrecedesCollidingExtensionSetup(t *testing.T) {
	cat := promptCatalog(map[string]string{"greet": "hi $1"})
	commands := extensions.NewCommandCatalog()
	if registered := registerPromptHostCommand(commands, cat, &bytes.Buffer{}); registered != promptCommandName {
		t.Fatalf("registered prompt command = %q, want %q", registered, promptCommandName)
	}
	ext := &promptCollidingExtension{}
	runtime := promptExtensionRuntime(t, ext)
	api := extensions.NewAPI(extensions.APIOptions{Commands: commands})
	runtime.SetAPI(func() sdk.API { return api })
	if err := runtime.Start(); err != nil {
		t.Fatalf("runtime.Start: %v", err)
	}
	canonical, ok := commands.Get(promptCommandName)
	if !ok || canonical.Description == "extension prompt" {
		t.Fatalf("canonical /prompt = %+v ok=%v", canonical, ok)
	}
	colliding, ok := commands.Get("prompt2")
	if !ok || colliding.Description != "extension prompt" {
		t.Fatalf("colliding extension = %+v ok=%v", colliding, ok)
	}
	aliases := registerPromptAliases(commands, cat, promptShorthandReserved)
	if aliases["greet"] != "greet" {
		t.Fatalf("aliases = %#v", aliases)
	}
	if _, ok := commands.Get("greet"); !ok {
		t.Fatal("prompt shorthand missing")
	}
}

func TestPromptCommandRegistrationWithoutCatalog(t *testing.T) {
	var commands *extensions.CommandCatalog
	aliases := registerPromptCommand(commands, promptCatalog(nil), nil, nil)
	if len(aliases) != 0 {
		t.Fatalf("aliases = %#v, want empty", aliases)
	}
}

func TestExpandPromptInput(t *testing.T) {
	cat := promptCatalog(map[string]string{"greet": "hi $1", "budget": "$@"})
	d := &runDeps{
		prompts:       cat,
		promptAliases: map[string]string{"greet": "greet"},
	}
	cases := []struct {
		name  string
		input string
		want  string
		err   string
	}{
		{name: "plain text unchanged", input: "plain text", want: "plain text"},
		{name: "other slash command unchanged", input: "/help", want: "/help"},
		{name: "lone slash unchanged", input: "/", want: "/"},
		{name: "alias invocation", input: "/greet Bob", want: "hi Bob"},
		{name: "explicit invocation", input: "/prompt greet Bob", want: "hi Bob"},
		{name: "explicit invocation with spaces", input: "  /prompt   greet   Bob  ", want: "hi Bob"},
		{name: "alias without arguments", input: "/greet", want: "hi "},
		{name: "alias with quoted argument", input: `/greet "two words"`, want: "hi two words"},
		{name: "missing template name", input: "/prompt", err: "template name is required"},
		{name: "missing template name with spaces", input: "/prompt   ", err: "template name is required"},
		{name: "unknown template", input: "/prompt missing x", err: "no prompt named"},
		{name: "unmatched quotes", input: `/prompt greet "unclosed`, err: "unmatched double quote"},
		{name: "budget error", input: "/prompt budget " + strings.Repeat("a", content.MaxExpandedPromptBytes+1), err: "1048576"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.expandPromptInput(tc.input)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandPromptInput: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expanded = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpandPromptInputAliasWithoutCatalogEntry(t *testing.T) {
	d := &runDeps{
		prompts:       content.NewPromptCatalog(content.Snapshot{}),
		promptAliases: map[string]string{"gone": "gone"},
	}
	got, err := d.expandPromptInput("/gone x")
	if err != nil {
		t.Fatalf("expandPromptInput: %v", err)
	}
	if got != "/gone x" {
		t.Fatalf("expanded = %q, want the original input", got)
	}
}

func TestExpandPromptInputZeroRunDeps(t *testing.T) {
	d := &runDeps{}
	got, err := d.expandPromptInput("plain")
	if err != nil || got != "plain" {
		t.Fatalf("plain = %q err=%v", got, err)
	}
	if _, err := d.expandPromptInput("/prompt x"); err == nil {
		t.Fatal("empty catalog must reject an explicit invocation")
	}
}
