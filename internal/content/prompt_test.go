package content

import (
	"runtime"
	"strings"
	"testing"
)

func TestParsePromptArguments(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "whitespace only", input: " \t\n ", want: nil},
		{name: "plain words", input: "one two three", want: []string{"one", "two", "three"}},
		{name: "runs of whitespace", input: "one\t\ntwo   three", want: []string{"one", "two", "three"}},
		{name: "double quoted group", input: `say "two words" now`, want: []string{"say", "two words", "now"}},
		{name: "single quoted group", input: `say 'two words' now`, want: []string{"say", "two words", "now"}},
		{name: "empty double quoted argument", input: `a "" b`, want: []string{"a", "", "b"}},
		{name: "empty single quoted argument", input: `""`, want: []string{""}},
		{name: "quoted whitespace preserved", input: `"  padded  "`, want: []string{"  padded  "}},
		{name: "escaped space outside quotes", input: `one\ two`, want: []string{"one two"}},
		{name: "escaped backslash outside quotes", input: `a\\b`, want: []string{`a\b`}},
		{name: "trailing backslash stays literal", input: `a\`, want: []string{`a\`}},
		{name: "escaped quote inside double quotes", input: `"say \"hi\""`, want: []string{`say "hi"`}},
		{name: "backslash literal inside single quotes", input: `'a\b'`, want: []string{`a\b`}},
		{name: "adjacent quoted segments", input: `"a"'b'c`, want: []string{"abc"}},
		{name: "unicode word", input: "héllo wörld", want: []string{"héllo", "wörld"}},
		{name: "unicode inside quotes", input: `"日本語 text" tail`, want: []string{"日本語 text", "tail"}},
		{name: "dollar values untouched", input: `"$1 $@ ${x}" $HOME`, want: []string{"$1 $@ ${x}", "$HOME"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePromptArguments(tc.input)
			if err != nil {
				t.Fatalf("ParsePromptArguments(%q): %v", tc.input, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("args = %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("args = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestParsePromptArgumentsRejectsUnmatchedQuotes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "open single", input: `one 'two`, want: "unmatched single quote"},
		{name: "lone single", input: `'`, want: "unmatched single quote"},
		{name: "open double", input: `one "two`, want: "unmatched double quote"},
		{name: "lone double", input: `"`, want: "unmatched double quote"},
		{name: "single inside double closes double", input: `"a'`, want: "unmatched double quote"},
		{name: "double inside single closes single", input: `'a"`, want: "unmatched single quote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePromptArguments(tc.input)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestExpandPromptTemplate(t *testing.T) {
	cases := []struct {
		name     string
		template string
		args     []string
		want     string
	}{
		{name: "no placeholders", template: "hello there", want: "hello there"},
		{name: "positional", template: "a $1 b", args: []string{"x"}, want: "a x b"},
		{name: "two positionals", template: "$1-$2", args: []string{"x", "y"}, want: "x-y"},
		{name: "missing positional is empty", template: "x$2y", args: []string{"only"}, want: "xy"},
		{name: "all args joined", template: "[$@]", args: []string{"a", "b c"}, want: "[a b c]"},
		{name: "arguments variable", template: "[$ARGUMENTS]", args: []string{"a", "b"}, want: "[a b]"},
		{name: "all args with no arguments", template: "[$@][$ARGUMENTS]", want: "[][]"},
		{name: "no args leaves positional empty", template: "[$1]", want: "[]"},
		{name: "unknown named variable literal", template: "$FOO ${BAR} $x", want: "$FOO ${BAR} $x"},
		{name: "arguments suffix is unknown", template: "$ARGUMENTSX", args: []string{"a"}, want: "$ARGUMENTSX"},
		{name: "arguments boundary before quote", template: "$ARGUMENTS'x'", args: []string{"a"}, want: "a'x'"},
		{name: "zero index literal", template: "$0 $01", args: []string{"a"}, want: "$0 a"},
		{name: "dollar at end literal", template: "price $", want: "price $"},
		{name: "dollar before unknown byte", template: "$% $", want: "$% $"},
		{name: "multi digit positional", template: "$10", args: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "ten"}, want: "ten"},
		{name: "missing multi digit positional empty", template: "a$10b", args: []string{"x"}, want: "ab"},
		{name: "unicode body preserved", template: "日本 $1 語", args: []string{"x"}, want: "日本 x 語"},
		{name: "math text preserved", template: `$1 + \(x_2\) = $2`, args: []string{"a", "b"}, want: `a + \(x_2\) = b`},
		{name: "code text preserved", template: "```go\nfmt.Println($1)\n```", args: []string{`"x"`}, want: "```go\nfmt.Println(\"x\")\n```"},
		{name: "shell payload preserved", template: "$(rm -rf /) `id` ; | && $1", args: []string{"ok"}, want: "$(rm -rf /) `id` ; | && ok"},
		{name: "no recursive substitution", template: "$1", args: []string{"$2"}, want: "$2"},
		{name: "no recursive arguments variable", template: "$ARGUMENTS", args: []string{"$@ $1"}, want: "$@ $1"},
		{name: "placeholder repeated", template: "$1 $1 $1", args: []string{"z"}, want: "z z z"},
		{name: "all args repeated", template: "[$@][$@]", args: []string{"a", "b"}, want: "[a b][a b]"},
		{name: "overflowing positional stays literal", template: "$99999999999999999999", args: []string{"a"}, want: "$99999999999999999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandPromptTemplate(tc.template, tc.args)
			if err != nil {
				t.Fatalf("ExpandPromptTemplate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("result = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExpandPromptTemplateBudget(t *testing.T) {
	exact := strings.Repeat("a", MaxExpandedPromptBytes)
	got, err := ExpandPromptTemplate("$@", []string{exact})
	if err != nil {
		t.Fatalf("exact budget: %v", err)
	}
	if len(got) != MaxExpandedPromptBytes {
		t.Fatalf("result length = %d, want %d", len(got), MaxExpandedPromptBytes)
	}

	over := strings.Repeat("a", MaxExpandedPromptBytes+1)
	if _, err := ExpandPromptTemplate("$@", []string{over}); err == nil {
		t.Fatal("over budget: want error")
	} else if !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("budget error = %q, want the limit", err)
	}

	half := strings.Repeat("b", MaxExpandedPromptBytes/2+1)
	if _, err := ExpandPromptTemplate("$1$1", []string{half}); err == nil {
		t.Fatal("repeated substitution over budget: want error")
	}

	if _, err := ExpandPromptTemplate("$ARGUMENTS", []string{over}); err == nil {
		t.Fatal("arguments substitution over budget: want error")
	}

	if _, err := ExpandPromptTemplate(strings.Repeat("c", MaxExpandedPromptBytes+1), nil); err == nil {
		t.Fatal("oversized literal body: want error")
	}
}

func TestExpandPromptTemplateBudgetBoundaries(t *testing.T) {
	atLimit := strings.Repeat("a", MaxExpandedPromptBytes)
	got, err := ExpandPromptTemplate(atLimit, nil)
	if err != nil {
		t.Fatalf("literal at the limit: %v", err)
	}
	if got != atLimit {
		t.Fatal("literal at the limit changed the body")
	}

	got, err = ExpandPromptTemplate(strings.Repeat("a", MaxExpandedPromptBytes-2)+"$1", []string{"xy"})
	if err != nil {
		t.Fatalf("mixed content exactly at the limit: %v", err)
	}
	if len(got) != MaxExpandedPromptBytes {
		t.Fatalf("mixed content length = %d, want %d", len(got), MaxExpandedPromptBytes)
	}

	if _, err := ExpandPromptTemplate(strings.Repeat("a", MaxExpandedPromptBytes-1)+"$1", []string{"xy"}); err == nil {
		t.Fatal("mixed literal and placeholder tail over the limit: want error")
	} else if !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("budget error = %q, want the limit", err)
	}

	if _, err := ExpandPromptTemplate("$1"+strings.Repeat("b", MaxExpandedPromptBytes), []string{"a"}); err == nil {
		t.Fatal("literal tail after a placeholder over the limit: want error")
	}

	got, err = ExpandPromptTemplate(strings.Repeat("a", MaxExpandedPromptBytes)+"$@", nil)
	if err != nil {
		t.Fatalf("empty joined tail at the limit: %v", err)
	}
	if len(got) != MaxExpandedPromptBytes {
		t.Fatalf("empty joined tail length = %d, want %d", len(got), MaxExpandedPromptBytes)
	}

	filled := "$@" + strings.Repeat("b", MaxExpandedPromptBytes-1)
	for _, template := range []string{
		atLimit + "$",
		atLimit + "$X",
		atLimit + "$0",
		filled + "$@",
		filled + "$ARGUMENTS",
	} {
		if _, err := ExpandPromptTemplate(template, []string{"z"}); err == nil {
			t.Fatalf("over-budget write for %q: want error", template)
		}
	}
}

func TestExpandPromptTemplateUnusedOversizedArguments(t *testing.T) {
	huge := strings.Repeat("x", 4*MaxExpandedPromptBytes)
	got, err := ExpandPromptTemplate("value=$1", []string{"used", huge})
	if err != nil {
		t.Fatalf("unused oversized argument: %v", err)
	}
	if got != "value=used" {
		t.Fatalf("result = %q, want %q", got, "value=used")
	}

	got, err = ExpandPromptTemplate("no placeholders", []string{huge, huge})
	if err != nil {
		t.Fatalf("unused arguments without placeholders: %v", err)
	}
	if got != "no placeholders" {
		t.Fatalf("result = %q, want %q", got, "no placeholders")
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := ExpandPromptTemplate("value=$1", []string{"used", huge}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if growth := after.TotalAlloc - before.TotalAlloc; growth >= uint64(len(huge))/2 {
		t.Fatalf("unused oversized argument allocated %d bytes", growth)
	}
}

func TestExpandPromptTemplateUsedOversizedArguments(t *testing.T) {
	huge := strings.Repeat("y", MaxExpandedPromptBytes+1)
	args := []string{"small", huge}

	if _, err := ExpandPromptTemplate("$2", args); err == nil {
		t.Fatal("used oversized positional: want error")
	} else if !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("budget error = %q, want the limit", err)
	}
	if args[0] != "small" || len(args[1]) != len(huge) {
		t.Fatal("expansion mutated the arguments")
	}

	got, err := ExpandPromptTemplate("$1", args)
	if err != nil {
		t.Fatalf("valid expansion after the error: %v", err)
	}
	if got != "small" {
		t.Fatalf("result = %q, want %q", got, "small")
	}

	if _, err := ExpandPromptTemplate("$@", args); err == nil {
		t.Fatal("joined oversized arguments: want error")
	}
	if _, err := ExpandPromptTemplate("$ARGUMENTS", []string{huge}); err == nil {
		t.Fatal("arguments variable over budget: want error")
	}
}

func TestPromptCatalogNamesAndLookup(t *testing.T) {
	snapshot := Snapshot{Prompts: map[string]PromptRef{
		"beta":  {Name: "beta", Package: "pkg", Content: "beta body"},
		"alpha": {Name: "alpha", Package: "pkg", Content: "alpha body"},
		"role/core": {
			Name: "role/core", Package: "pkg", Path: "role/core.md", Content: "core body", Tier: TierUser, Origin: "user:prompts",
		},
	}}
	cat := NewPromptCatalog(snapshot)
	names := cat.Names()
	want := []string{"alpha", "beta", "role/core"}
	if len(names) != len(want) {
		t.Fatalf("Names = %#v, want %#v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Names = %#v, want %#v", names, want)
		}
	}
	ref, ok := cat.Lookup("role/core")
	if !ok || ref.Content != "core body" || ref.Tier != TierUser || ref.Origin != "user:prompts" {
		t.Fatalf("Lookup = %+v ok=%v", ref, ok)
	}
	if _, ok := cat.Lookup("missing"); ok {
		t.Fatal("Lookup found a missing prompt")
	}
}

func TestPromptCatalogInfoDescriptions(t *testing.T) {
	long := strings.Repeat("x", 100)
	snapshot := Snapshot{Prompts: map[string]PromptRef{
		"first":    {Name: "first", Content: "\n\n# Daily notes\nmore body"},
		"long":     {Name: "long", Content: long},
		"control":  {Name: "control", Content: "safe\x1b[31m line\nnext"},
		"empty":    {Name: "empty", Content: "   \n\t\n"},
		"deferred": {Name: "deferred", Package: "pkg"},
	}}
	cat := NewPromptCatalog(snapshot)
	infos := cat.Infos()
	if len(infos) != 5 {
		t.Fatalf("Infos = %d entries, want 5", len(infos))
	}
	info, ok := cat.Info("first")
	if !ok || info.Description != "# Daily notes" {
		t.Fatalf("first info = %+v ok=%v", info, ok)
	}
	info, _ = cat.Info("long")
	if len([]rune(info.Description)) != promptDescriptionRunes {
		t.Fatalf("long description runes = %d, want %d", len([]rune(info.Description)), promptDescriptionRunes)
	}
	info, _ = cat.Info("control")
	if strings.ContainsAny(info.Description, "\x1b") {
		t.Fatalf("control description = %q, want controls stripped", info.Description)
	}
	if info.Description != "safe[31m line" {
		t.Fatalf("control description = %q", info.Description)
	}
	info, _ = cat.Info("empty")
	if info.Description != "" {
		t.Fatalf("empty description = %q", info.Description)
	}
	infos[0].Description = "mutated"
	if again, _ := cat.Info(infos[0].Name); again.Description == "mutated" {
		t.Fatal("Infos must return a copy")
	}
}

func TestPromptCatalogRejectsControlBearingNames(t *testing.T) {
	snapshot := Snapshot{Prompts: map[string]PromptRef{
		"good":            {Name: "good", Content: "ok"},
		"bad\x1b[31m":     {Name: "bad\x1b[31m", Content: "escape"},
		"bad\nline":       {Name: "bad\nline", Content: "newline"},
		"bad\x7f":         {Name: "bad\x7f", Content: "delete"},
		"bad\u202e":       {Name: "bad\u202e", Content: "bidi"},
		"bad space":       {Name: "bad space", Content: "space"},
		"bad\u0080h":      {Name: "bad\u0080h", Content: "c1"},
		"\xff\xfeinvalid": {Name: "\xff\xfeinvalid", Content: "utf8"},
	}}
	cat := NewPromptCatalog(snapshot)
	names := cat.Names()
	if len(names) != 1 || names[0] != "good" {
		t.Fatalf("Names = %#v, want only good", names)
	}
	for _, name := range []string{"bad\x1b[31m", "bad\nline", "bad\x7f", "bad\u202e", "bad space", "bad\u0080h", "\xff\xfeinvalid"} {
		if _, ok := cat.Lookup(name); ok {
			t.Fatalf("Lookup(%q) resolved a rejected name", name)
		}
	}
	if len(cat.Infos()) != 1 {
		t.Fatalf("Infos = %#v, want only the safe prompt", cat.Infos())
	}
}

func TestPromptCatalogEntries(t *testing.T) {
	snapshot := Snapshot{Prompts: map[string]PromptRef{
		"b": {Name: "b", Content: "body b"},
		"a": {Name: "a", Content: "body a"},
	}}
	cat := NewPromptCatalog(snapshot)
	entries := cat.Entries()
	if len(entries) != 2 {
		t.Fatalf("Entries = %#v", entries)
	}
	if entries[0].Info.Name != "a" || entries[0].Ref.Content != "body a" {
		t.Fatalf("Entries[0] = %+v", entries[0])
	}
	if entries[1].Info.Name != "b" || entries[1].Ref.Content != "body b" {
		t.Fatalf("Entries[1] = %+v", entries[1])
	}
}

func TestPromptCatalogEmpty(t *testing.T) {
	cat := NewPromptCatalog(Snapshot{})
	if len(cat.Names()) != 0 || len(cat.Infos()) != 0 {
		t.Fatal("empty snapshot must produce an empty catalog")
	}
	if _, ok := cat.Lookup("anything"); ok {
		t.Fatal("empty catalog resolved a prompt")
	}
	if _, ok := cat.Info("anything"); ok {
		t.Fatal("empty catalog returned info")
	}
}

func TestPromptCatalogTrustAndPrecedence(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	writeTree(t, ws, map[string]string{".smidja/prompts/local.md": "workspace"})
	writeTree(t, home, map[string]string{".smidja/prompts/shared.md": "user"})
	writeTree(t, first, map[string]string{"prompts/shared.md": "package one", "prompts/first.md": "first"})
	writeTree(t, second, map[string]string{"prompts/shared.md": "package two", "prompts/second.md": "second"})

	base := Options{
		BundleID:     "bundle",
		WorkspaceDir: ws,
		UserHome:     home,
		PackagesDirs: []string{first, second},
	}
	untrusted, err := Load(Options{BundleID: "bundle", WorkspaceDir: ws, UserHome: home, PackagesDirs: []string{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	untrustedCat := NewPromptCatalog(untrusted)
	if _, ok := untrustedCat.Lookup("local"); ok {
		t.Fatal("untrusted workspace prompt must be excluded")
	}
	if ref, _ := untrustedCat.Lookup("shared"); ref.Content != "user" {
		t.Fatalf("shared = %q, want the user tier", ref.Content)
	}

	trusted := base
	trusted.TrustWorkspace = true
	snapshot, err := Load(trusted)
	if err != nil {
		t.Fatal(err)
	}
	cat := NewPromptCatalog(snapshot)
	if ref, ok := cat.Lookup("local"); !ok || ref.Content != "workspace" || ref.Tier != TierWorkspace {
		t.Fatalf("local = %+v ok=%v", ref, ok)
	}
	if ref, _ := cat.Lookup("shared"); ref.Content != "user" || ref.Tier != TierUser {
		t.Fatalf("shared = %+v, want the user tier", ref)
	}
	if ref, _ := cat.Lookup("first"); ref.Content != "first" {
		t.Fatalf("first = %+v", ref)
	}
	if ref, _ := cat.Lookup("second"); ref.Content != "second" {
		t.Fatalf("second = %+v", ref)
	}
}
