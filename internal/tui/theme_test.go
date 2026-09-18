package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testThemeJSON = `{
  "name": "custom",
  "vars": {
    "primary": "#ff0000",
    "secondary": "primary"
  },
  "colors": {
    "accent": "secondary",
    "border": "#00ff00",
    "borderAccent": 42,
    "borderMuted": "",
    "success": "primary",
    "error": "#ff0000",
    "warning": "#ffff00",
    "muted": "#808080",
    "dim": "#404040",
    "text": "#eeeeee",
    "thinkingText": "#606060",
    "selectedBg": "#101020",
    "userMessageBg": "#202030",
    "userMessageText": "#ffffff",
    "customMessageBg": "#302040",
    "customMessageText": "#ffffff",
    "customMessageLabel": "#aa00aa",
    "toolPendingBg": "#111111",
    "toolSuccessBg": "#112211",
    "toolErrorBg": "#221111",
    "toolTitle": "#eeeeee",
    "toolOutput": "#909090",
    "mdHeading": "#f0c674",
    "mdLink": "#81a2be",
    "mdLinkUrl": "#666666",
    "mdCode": "#8abeb7",
    "mdCodeBlock": "#b5bd68",
    "mdCodeBlockBorder": "#808080",
    "mdQuote": "#707070",
    "mdQuoteBorder": "#707070",
    "mdHr": "#707070",
    "mdListBullet": "#8abeb7",
    "toolDiffAdded": "#b5bd68",
    "toolDiffRemoved": "#cc6666",
    "toolDiffContext": "#808080",
    "syntaxComment": "#6A9955",
    "syntaxKeyword": "#569CD6",
    "syntaxFunction": "#DCDCAA",
    "syntaxVariable": "#9CDCFE",
    "syntaxString": "#CE9178",
    "syntaxNumber": "#B5CEA8",
    "syntaxType": "#4EC9B0",
    "syntaxOperator": "#D4D4D4",
    "syntaxPunctuation": "#D4D4D4",
    "thinkingOff": "#505050",
    "thinkingMinimal": "#6e6e6e",
    "thinkingLow": "#5f87af",
    "thinkingMedium": "#81a2be",
    "thinkingHigh": "#b294bb",
    "thinkingXhigh": "#d183e8",
    "bashMode": "#b5bd68"
  }
}`

func writeThemeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestThemeValidationMissingTokens(t *testing.T) {
	document := mustTestTheme(testThemeJSON)
	delete(document.Colors, "accent")
	if err := validateThemeDocument("broken", document); err == nil {
		t.Fatal("missing accent token should fail validation")
	} else if !strings.Contains(err.Error(), "accent") {
		t.Fatalf("validation error should mention accent: %v", err)
	}
}

func TestThemeValidationNameSlash(t *testing.T) {
	document, _ := parseTestTheme(testThemeJSON)
	document.Name = "a/b"
	if err := validateThemeDocument("slashed", document); err == nil {
		t.Fatal("theme name with slash should fail validation")
	}
}

func TestThemeParsingVariants(t *testing.T) {
	if _, err := parseThemeJSON("bad", "{invalid"); err == nil {
		t.Fatal("invalid JSON should fail")
	}
	if _, err := parseThemeJSON("empty", `{"name":"x"}`); err == nil {
		t.Fatal("missing colors should fail")
	}

	theme, err := newTheme(mustTestTheme(testThemeJSON), ColorModeTrueColor, "/tmp/custom.json")
	if err != nil {
		t.Fatalf("newTheme: %v", err)
	}
	if theme.Name != "custom" || theme.SourcePath != "/tmp/custom.json" {
		t.Fatalf("theme identity = %q %q", theme.Name, theme.SourcePath)
	}

	if got := theme.Fg("accent", "X"); got != SGRFgRGB(255, 0, 0)+"X"+SGRFgDefault {
		t.Fatalf("var chain accent = %q", got)
	}
	if got := theme.Fg("border", "X"); got != SGRFgRGB(0, 255, 0)+"X"+SGRFgDefault {
		t.Fatalf("hex border = %q", got)
	}
	if got := theme.Fg("borderAccent", "X"); got != SGRFg256(42)+"X"+SGRFgDefault {
		t.Fatalf("indexed borderAccent = %q", got)
	}
	if got := theme.Fg("borderMuted", "X"); got != SGRFgDefault+"X"+SGRFgDefault {
		t.Fatalf("default color = %q", got)
	}
	if got := theme.Bg("selectedBg", "X"); got != SGRBgRGB(16, 16, 32)+"X"+SGRBgDefault {
		t.Fatalf("bg = %q", got)
	}
}

func TestThemeFallbacks(t *testing.T) {
	theme, err := newTheme(mustTestTheme(testThemeJSON), ColorModeTrueColor, "")
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := theme.GetFgAnsi("thinkingXhigh")
	got, _ := theme.GetFgAnsi("thinkingMax")
	if got != expected {
		t.Fatalf("thinkingMax = %q, want thinkingXhigh %q", got, expected)
	}
	expectedThumb, _ := theme.GetFgAnsi("text")
	gotThumb, _ := theme.GetFgAnsi("scrollbarThumb")
	if gotThumb != expectedThumb {
		t.Fatalf("scrollbarThumb fallback = %q, want text %q", gotThumb, expectedThumb)
	}
	expectedTrack, _ := theme.GetFgAnsi("muted")
	gotTrack, _ := theme.GetFgAnsi("scrollbarTrack")
	if gotTrack != expectedTrack {
		t.Fatalf("scrollbarTrack fallback = %q, want muted %q", gotTrack, expectedTrack)
	}
	expectedSearchBg, _ := theme.GetBgAnsi("selectedBg")
	gotSearchBg, _ := theme.GetBgAnsi("searchMatchBg")
	if gotSearchBg != expectedSearchBg {
		t.Fatalf("searchMatchBg fallback mismatch")
	}
	expectedSearchText, _ := theme.GetFgAnsi("text")
	gotSearchText, _ := theme.GetFgAnsi("searchMatchText")
	if gotSearchText != expectedSearchText {
		t.Fatalf("searchMatchText fallback mismatch")
	}
	if theme.Fallback("thinkingMax") != "thinkingXhigh" {
		t.Fatal("Fallback(thinkingMax) mismatch")
	}
}

func TestThemeVarErrors(t *testing.T) {
	cyclic := `{"name":"cyclic","vars":{"a":"b","b":"a"},"colors":{"accent":"a","border":"#000000","borderAccent":"#000000","borderMuted":"#000000","success":"#000000","error":"#000000","warning":"#000000","muted":"#000000","dim":"#000000","text":"#000000","thinkingText":"#000000","selectedBg":"#000000","userMessageBg":"#000000","userMessageText":"#000000","customMessageBg":"#000000","customMessageText":"#000000","customMessageLabel":"#000000","toolPendingBg":"#000000","toolSuccessBg":"#000000","toolErrorBg":"#000000","toolTitle":"#000000","toolOutput":"#000000","mdHeading":"#000000","mdLink":"#000000","mdLinkUrl":"#000000","mdCode":"#000000","mdCodeBlock":"#000000","mdCodeBlockBorder":"#000000","mdQuote":"#000000","mdQuoteBorder":"#000000","mdHr":"#000000","mdListBullet":"#000000","toolDiffAdded":"#000000","toolDiffRemoved":"#000000","toolDiffContext":"#000000","syntaxComment":"#000000","syntaxKeyword":"#000000","syntaxFunction":"#000000","syntaxVariable":"#000000","syntaxString":"#000000","syntaxNumber":"#000000","syntaxType":"#000000","syntaxOperator":"#000000","syntaxPunctuation":"#000000","thinkingOff":"#000000","thinkingMinimal":"#000000","thinkingLow":"#000000","thinkingMedium":"#000000","thinkingHigh":"#000000","thinkingXhigh":"#000000","bashMode":"#000000"}}`
	_, err := newTheme(mustTestTheme(cyclic), ColorModeTrueColor, "")
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("cyclic vars should fail: %v", err)
	}

	unknown := strings.Replace(testThemeJSON, `"accent": "secondary"`, `"accent": "missingVar"`, 1)
	_, err = newTheme(mustTestTheme(unknown), ColorModeTrueColor, "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown var should fail: %v", err)
	}

	badIndex := strings.Replace(testThemeJSON, `"borderAccent": 42`, `"borderAccent": 999`, 1)
	if _, err := parseTestTheme(badIndex); err == nil {
		t.Fatal("out-of-range color index should fail to parse")
	}
}

func TestTheme256ModeConversion(t *testing.T) {
	theme, err := newTheme(mustTestTheme(testThemeJSON), ColorMode256, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := theme.GetFgAnsi("accent")
	if !strings.HasPrefix(got, "\x1b[38;5;") {
		t.Fatalf("256-mode accent = %q", got)
	}
	if strings.Contains(got, ";2;") {
		t.Fatalf("256 mode should not emit rgb: %q", got)
	}
}

func TestThemeStyles(t *testing.T) {
	theme, _ := newTheme(mustTestTheme(testThemeJSON), ColorModeTrueColor, "")
	if theme.Bold("x") != SGRBold+"x"+SGRBoldOff {
		t.Fatal("Bold mismatch")
	}
	if theme.Italic("x") != SGRItalic+"x"+SGRItalicOff {
		t.Fatal("Italic mismatch")
	}
	if theme.Underline("x") != SGRUnderline+"x"+SGRUnderlineOff {
		t.Fatal("Underline mismatch")
	}
	if theme.Inverse("x") != SGRInverse+"x"+SGRInverseOff {
		t.Fatal("Inverse mismatch")
	}
	if theme.Strike("x") != SGRStrike+"x"+SGRStrikeOff {
		t.Fatal("Strike mismatch")
	}
	if got, _ := theme.GetFgAnsi("nonexistent"); got != "" {
		t.Fatal("unknown token should return empty")
	}
}

func TestBuiltinThemesValid(t *testing.T) {
	for _, name := range []string{"dark", "light"} {
		registry := NewThemeRegistry(t.TempDir(), t.TempDir(), ColorModeTrueColor)
		theme, err := registry.SetTheme(name)
		if err != nil {
			t.Fatalf("builtin theme %s failed: %v", name, err)
		}
		if theme.Name != name {
			t.Fatalf("theme name = %q, want %q", theme.Name, name)
		}
		if _, ok := theme.GetFgAnsi(tokenAccent); !ok {
			t.Fatalf("theme %s missing accent", name)
		}
	}
}

func TestThemeRegistryPrecedence(t *testing.T) {
	home := t.TempDir()
	userDir := UserThemesDir(home)
	packageDir := t.TempDir()
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}

	packageTheme := strings.Replace(testThemeJSON, `"name": "custom"`, `"name": "overlap"`, 1)
	writeThemeFile(t, packageDir, "overlap.json", packageTheme)

	userVariant := strings.Replace(testThemeJSON, `"name": "custom"`, `"name": "overlap"`, 1)
	userVariant = strings.Replace(userVariant, `"primary": "#ff0000"`, `"primary": "#00ffff"`, 1)
	writeThemeFile(t, userDir, "overlap.json", userVariant)

	writeThemeFile(t, userDir, "useronly.json", strings.Replace(testThemeJSON, `"name": "custom"`, `"name": "useronly"`, 1))

	registry := NewThemeRegistry(userDir, packageDir, ColorModeTrueColor)
	themes := registry.AvailableThemes()
	names := make([]string, 0, len(themes))
	for _, source := range themes {
		names = append(names, source.Name)
	}
	for _, want := range []string{"dark", "light", "overlap", "useronly"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("theme %q missing from %v", want, names)
		}
	}

	overlap := findThemeSource(themes, "overlap")
	if overlap == "" || !strings.HasPrefix(overlap, userDir) {
		t.Fatalf("user theme must win precedence, got path %q", overlap)
	}

	theme, err := registry.SetTheme("overlap")
	if err != nil {
		t.Fatal(err)
	}
	if got := theme.Fg("accent", "x"); !strings.Contains(got, SGRFgRGB(0, 255, 255)) {
		t.Fatalf("user variant accent = %q, want cyan", got)
	}

	if _, err := registry.SetTheme("nonexistent"); err == nil {
		t.Fatal("unknown theme should fail")
	}
}

func TestThemeRegistryHotReload(t *testing.T) {
	home := t.TempDir()
	userDir := UserThemesDir(home)
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeThemeFile(t, userDir, "hot.json", strings.Replace(testThemeJSON, `"name": "custom"`, `"name": "hot"`, 1))

	registry := NewThemeRegistry(userDir, t.TempDir(), ColorModeTrueColor)
	defer registry.StopWatching()
	reloaded := make(chan struct{}, 4)
	registry.OnReload(func() { reloaded <- struct{}{} })

	if _, err := registry.SetTheme("hot"); err != nil {
		t.Fatal(err)
	}
	registry.StartWatching(5 * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	updated := strings.Replace(strings.Replace(testThemeJSON, `"primary": "#ff0000"`, `"primary": "#00ff88"`, 1), `"name": "custom"`, `"name": "hot"`, 1)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("hot reload callback not fired")
	}
	theme := registry.Active()
	if got := theme.Fg("accent", "x"); !strings.Contains(got, SGRFgRGB(0, 255, 136)) {
		t.Fatalf("reloaded accent = %q", got)
	}

	registry.StopWatching()
	select {
	case <-reloaded:
	default:
	}
}

func TestThemeRegistryBuiltinNotWatched(t *testing.T) {
	registry := NewThemeRegistry(t.TempDir(), t.TempDir(), ColorModeTrueColor)
	defer registry.StopWatching()
	if _, err := registry.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	registry.StartWatching(5 * time.Millisecond)
	if registry.ActiveName() != "dark" {
		t.Fatal("active theme mismatch")
	}
	if _, changed := registry.ReloadActive(); changed {
		t.Fatal("builtin theme reload should be a no-op")
	}
}

func TestColorModeDetection(t *testing.T) {
	SetColorModeOverride(ColorModeUnset)
	t.Cleanup(func() { SetColorModeOverride(ColorModeUnset) })
	t.Setenv("COLORTERM", "truecolor")
	if DetectColorMode() != ColorModeTrueColor {
		t.Fatal("truecolor not detected")
	}
	t.Setenv("COLORTERM", "24bit")
	if DetectColorMode() != ColorModeTrueColor {
		t.Fatal("24bit not detected")
	}
	t.Setenv("COLORTERM", "")
	if DetectColorMode() != ColorMode256 {
		t.Fatal("default should be 256")
	}
	SetColorModeOverride(ColorModeTrueColor)
	if DetectColorMode() != ColorModeTrueColor {
		t.Fatal("override not honored")
	}
}

func unmarshalTestJSON(content string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimLeft(content, "\ufeff")))
	return decoder.Decode(target)
}

func parseTestTheme(content string) (themeJSON, error) {
	return parseThemeJSON("test", content)
}

func mustTestTheme(content string) themeJSON {
	document, err := parseTestTheme(content)
	if err != nil {
		panic(err)
	}
	return document
}

func findThemeSource(sources []ThemeSource, name string) string {
	for _, source := range sources {
		if source.Name == name {
			return source.Path
		}
	}
	return ""
}
