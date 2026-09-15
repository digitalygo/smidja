package tui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

//go:embed theme_dark.json
var themeDarkJSON string

//go:embed theme_light.json
var themeLightJSON string

type ThemeColor string

const tokenAccent = ThemeColor("accent")

type ColorMode int

const (
	ColorModeTrueColor ColorMode = iota
	ColorMode256
)

type themeColorValue struct {
	text   string
	number int
	isNum  bool
}

func (v themeColorValue) MarshalJSON() ([]byte, error) {
	if v.isNum {
		return json.Marshal(v.number)
	}
	return json.Marshal(v.text)
}

func (v *themeColorValue) UnmarshalJSON(data []byte) error {
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		if number != math.Trunc(number) || number < 0 || number > 255 {
			return fmt.Errorf("color index %v out of range 0-255", number)
		}
		v.number = int(number)
		v.isNum = true
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("color value must be a hex string like \"#ff0000\", an empty string, or a 256-color index: %w", err)
	}
	v.text = text
	v.isNum = false
	return nil
}

type themeJSON struct {
	Name   string                     `json:"name"`
	Vars   map[string]themeColorValue `json:"vars,omitempty"`
	Colors map[string]themeColorValue `json:"colors"`
}

var themeFgTokens = []ThemeColor{
	"accent", "border", "borderAccent", "borderMuted", "success", "error",
	"warning", "muted", "dim", "text", "thinkingText",
	"scrollbarTrack", "scrollbarThumb",
	"userMessageText", "customMessageText", "customMessageLabel",
	"toolTitle", "toolOutput",
	"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock",
	"mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
	"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
	"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable",
	"syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
	"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium",
	"thinkingHigh", "thinkingXhigh", "thinkingMax",
	"bashMode",
}

var themeBgTokens = []ThemeColor{
	"selectedBg", "searchMatchBg",
	"userMessageBg", "customMessageBg",
	"toolPendingBg", "toolSuccessBg", "toolErrorBg",
}

var requiredThemeTokens = func() []ThemeColor {
	required := make([]ThemeColor, 0, len(themeFgTokens)+len(themeBgTokens))
	for _, token := range themeFgTokens {
		if token != "scrollbarTrack" && token != "scrollbarThumb" && token != "thinkingMax" {
			required = append(required, token)
		}
	}
	for _, token := range themeBgTokens {
		if token != "searchMatchBg" {
			required = append(required, token)
		}
	}
	return required
}()

type themeFallback struct {
	from ThemeColor
	to   ThemeColor
}

var themeTokenFallbacks = []themeFallback{
	{from: "scrollbarTrack", to: "muted"},
	{from: "scrollbarThumb", to: "text"},
	{from: "thinkingMax", to: "thinkingXhigh"},
	{from: "searchMatchBg", to: "selectedBg"},
	{from: "searchMatchText", to: "text"},
}

var cubeValues = [6]int{0, 95, 135, 175, 215, 255}

func grayValues() [24]int {
	var values [24]int
	for i := range values {
		values[i] = 8 + i*10
	}
	return values
}

func hexToRGB(hex string) (int, int, int, error) {
	cleaned := strings.TrimPrefix(hex, "#")
	if len(cleaned) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex color: %s", hex)
	}
	parsed, err := strconv.ParseUint(cleaned, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid hex color: %s", hex)
	}
	return int(parsed>>16) & 0xff, int(parsed>>8) & 0xff, int(parsed) & 0xff, nil
}

func closestIndex(value int, candidates []int) int {
	best := 0
	bestDist := math.MaxInt
	for i, candidate := range candidates {
		dist := value - candidate
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			best = i
		}
	}
	return best
}

func colorDistance(r1, g1, b1, r2, g2, b2 int) int {
	dr := r1 - r2
	dg := g1 - g2
	db := b1 - b2
	return dr*dr*299/1000 + dg*dg*587/1000 + db*db*114/1000
}

func rgbTo256(r, g, b int) int {
	rIdx := closestIndex(r, cubeValues[:])
	gIdx := closestIndex(g, cubeValues[:])
	bIdx := closestIndex(b, cubeValues[:])
	cubeIndex := 16 + 36*rIdx + 6*gIdx + bIdx
	cubeDist := colorDistance(r, g, b, cubeValues[rIdx], cubeValues[gIdx], cubeValues[bIdx])

	grays := grayValues()
	luma := (r*299 + g*587 + b*114) / 1000
	grayIdx := closestIndex(luma, grays[:])
	grayIndex := 232 + grayIdx
	grayDist := colorDistance(r, g, b, grays[grayIdx], grays[grayIdx], grays[grayIdx])

	maxChannel := maxInt(maxInt(r, g), b)
	minChannel := minInt(minInt(r, g), b)
	if maxChannel-minChannel < 10 && grayDist < cubeDist {
		return grayIndex
	}
	return cubeIndex
}

func hexTo256(hex string) (int, error) {
	r, g, b, err := hexToRGB(hex)
	if err != nil {
		return 0, err
	}
	return rgbTo256(r, g, b), nil
}

func fgANSI(value themeColorValue, mode ColorMode) (string, error) {
	if !value.isNum && value.text == "" {
		return SGRFgDefault, nil
	}
	if value.isNum {
		return SGRFg256(value.number), nil
	}
	if strings.HasPrefix(value.text, "#") {
		if mode == ColorModeTrueColor {
			r, g, b, err := hexToRGB(value.text)
			if err != nil {
				return "", err
			}
			return SGRFgRGB(r, g, b), nil
		}
		index, err := hexTo256(value.text)
		if err != nil {
			return "", err
		}
		return SGRFg256(index), nil
	}
	return "", fmt.Errorf("invalid color value: %s", value.text)
}

func bgANSI(value themeColorValue, mode ColorMode) (string, error) {
	if !value.isNum && value.text == "" {
		return SGRBgDefault, nil
	}
	if value.isNum {
		return SGRBg256(value.number), nil
	}
	if strings.HasPrefix(value.text, "#") {
		if mode == ColorModeTrueColor {
			r, g, b, err := hexToRGB(value.text)
			if err != nil {
				return "", err
			}
			return SGRBgRGB(r, g, b), nil
		}
		index, err := hexTo256(value.text)
		if err != nil {
			return "", err
		}
		return SGRBg256(index), nil
	}
	return "", fmt.Errorf("invalid color value: %s", value.text)
}

func resolveVarRef(value themeColorValue, vars map[string]themeColorValue, visited map[string]struct{}) (themeColorValue, error) {
	if value.isNum || value.text == "" || strings.HasPrefix(value.text, "#") {
		return value, nil
	}
	if _, cyclic := visited[value.text]; cyclic {
		return themeColorValue{}, fmt.Errorf("circular variable reference detected: %s", value.text)
	}
	resolved, ok := vars[value.text]
	if !ok {
		return themeColorValue{}, fmt.Errorf("variable reference not found: %s", value.text)
	}
	visited[value.text] = struct{}{}
	resolved, err := resolveVarRef(resolved, vars, visited)
	delete(visited, value.text)
	return resolved, err
}

func parseThemeJSON(label, content string) (themeJSON, error) {
	var parsed themeJSON
	decoder := json.NewDecoder(strings.NewReader(strings.TrimLeft(content, "\ufeff")))
	if err := decoder.Decode(&parsed); err != nil {
		return themeJSON{}, fmt.Errorf("failed to parse theme %s: %w", label, err)
	}
	if parsed.Colors == nil {
		return themeJSON{}, fmt.Errorf("invalid theme %q: expected an object with a \"colors\" map", label)
	}
	return parsed, nil
}

func validateThemeDocument(label string, document themeJSON) error {
	if strings.Contains(document.Name, "/") {
		return fmt.Errorf("invalid theme name %q: theme names cannot contain \"/\" because it is reserved for automatic light/dark theme settings", document.Name)
	}
	if document.Colors == nil {
		return fmt.Errorf("invalid theme %q: expected an object with a \"colors\" map", label)
	}
	var missing []string
	present := make(map[string]struct{}, len(document.Colors))
	for token := range document.Colors {
		present[token] = struct{}{}
	}
	for _, token := range requiredThemeTokens {
		if _, ok := present[string(token)]; !ok {
			missing = append(missing, string(token))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid theme %q:\n\nMissing required color tokens:\n  - %s\n\nPlease add these colors to your theme's \"colors\" object.\nSee the built-in themes (dark.json, light.json) for reference values",
			label, strings.Join(missing, "\n  - "))
	}
	return nil
}

type Theme struct {
	Name       string
	SourcePath string

	mode     ColorMode
	fgColors map[ThemeColor]string
	bgColors map[ThemeColor]string
}

func newTheme(document themeJSON, mode ColorMode, sourcePath string) (*Theme, error) {
	if err := validateThemeDocument(document.Name, document); err != nil {
		return nil, err
	}
	if mode == ColorModeUnset {
		mode = DetectColorMode()
	}
	vars := document.Vars
	if vars == nil {
		vars = map[string]themeColorValue{}
	}
	colors := make(map[string]themeColorValue, len(document.Colors)+len(themeTokenFallbacks))
	for token, value := range document.Colors {
		colors[token] = value
	}
	for _, fallback := range themeTokenFallbacks {
		if _, ok := colors[string(fallback.from)]; !ok {
			if base, ok := document.Colors[string(fallback.to)]; ok {
				colors[string(fallback.from)] = base
			}
		}
	}

	theme := &Theme{
		Name:       document.Name,
		SourcePath: sourcePath,
		mode:       mode,
		fgColors:   make(map[ThemeColor]string, len(themeFgTokens)),
		bgColors:   make(map[ThemeColor]string, len(themeBgTokens)),
	}
	bgSet := make(map[ThemeColor]struct{}, len(themeBgTokens))
	for _, token := range themeBgTokens {
		bgSet[token] = struct{}{}
	}
	for token, rawValue := range colors {
		value, err := resolveVarRef(rawValue, vars, map[string]struct{}{})
		if err != nil {
			return nil, err
		}
		color := ThemeColor(token)
		ansi, err := fgANSI(value, mode)
		if err != nil {
			return nil, err
		}
		if _, isBg := bgSet[color]; isBg {
			ansi, err = bgANSI(value, mode)
			if err != nil {
				return nil, err
			}
			theme.bgColors[color] = ansi
		} else {
			theme.fgColors[color] = ansi
		}
	}
	return theme, nil
}

func (t *Theme) Fg(token ThemeColor, text string) string {
	ansi, ok := t.fgColors[token]
	if !ok {
		ansi = SGRFgDefault
	}
	return ansi + text + SGRFgDefault
}

func (t *Theme) Bg(token ThemeColor, text string) string {
	ansi, ok := t.bgColors[token]
	if !ok {
		ansi = SGRBgDefault
	}
	return ansi + text + SGRBgDefault
}

func (t *Theme) GetFgAnsi(token ThemeColor) (string, bool) {
	ansi, ok := t.fgColors[token]
	return ansi, ok
}

func (t *Theme) GetBgAnsi(token ThemeColor) (string, bool) {
	ansi, ok := t.bgColors[token]
	return ansi, ok
}

func (t *Theme) Bold(text string) string      { return SGRBold + text + SGRBoldOff }
func (t *Theme) Italic(text string) string    { return SGRItalic + text + SGRItalicOff }
func (t *Theme) Underline(text string) string { return SGRUnderline + text + SGRUnderlineOff }
func (t *Theme) Inverse(text string) string   { return SGRInverse + text + SGRInverseOff }
func (t *Theme) Strike(text string) string    { return SGRStrike + text + SGRStrikeOff }

func (t *Theme) Fallback(token ThemeColor) ThemeColor {
	for _, fallback := range themeTokenFallbacks {
		if fallback.from == token {
			return fallback.to
		}
	}
	return token
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
