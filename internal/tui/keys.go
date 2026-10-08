package tui

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

var kittyProtocolActive atomic.Bool

func SetKittyProtocolActive(active bool) { kittyProtocolActive.Store(active) }

func KittyProtocolActive() bool { return kittyProtocolActive.Load() }

type keyEventType int

const (
	eventPress keyEventType = iota
	eventRepeat
	eventRelease
)

type parsedKittySequence struct {
	codepoint     int
	shiftedKey    int
	baseLayoutKey int
	modifier      int
	eventType     keyEventType
	hasShifted    bool
	hasBaseLayout bool
}

type parsedModifyOtherKeys struct {
	codepoint int
	modifier  int
}

var (
	kittyCSIURegex  = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
	kittyArrowRegex = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$`)
	kittyFuncRegex  = regexp.MustCompile(`^\x1b\[(\d+)(?:;(\d+))?(?::(\d+))?~$`)
	kittyHFRegex    = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([HF])$`)
	modOtherRegex   = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)
	sgrMouseRe      = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)[Mm]$`)
	oldMouseRe      = regexp.MustCompile(`^\x1b\[M([\x20-\x7e]{3})$`)
)

func parseEventType(value string) keyEventType {
	if value == "" {
		return eventPress
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return eventPress
	}
	switch n {
	case 2:
		return eventRepeat
	case 3:
		return eventRelease
	}
	return eventPress
}

func parseIntOr(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func parseKittySequence(data string) (parsedKittySequence, bool) {
	if m := kittyCSIURegex.FindStringSubmatch(data); m != nil {
		parsed := parsedKittySequence{
			codepoint: parseIntOr(m[1], 0),
			shiftedKey: func() int {
				if m[2] == "" {
					return 0
				}
				return parseIntOr(m[2], 0)
			}(),
			baseLayoutKey: parseIntOr(m[3], 0),
			modifier:      parseIntOr(m[4], 1) - 1,
			eventType:     parseEventType(m[5]),
			hasShifted:    m[2] != "",
			hasBaseLayout: m[3] != "",
		}
		return parsed, true
	}
	if m := kittyArrowRegex.FindStringSubmatch(data); m != nil {
		codepoints := map[byte]int{'A': cpArrowUp, 'B': cpArrowDown, 'C': cpArrowRight, 'D': cpArrowLeft}
		return parsedKittySequence{
			codepoint: codepoints[m[3][0]],
			modifier:  parseIntOr(m[1], 1) - 1,
			eventType: parseEventType(m[2]),
		}, true
	}
	if m := kittyFuncRegex.FindStringSubmatch(data); m != nil {
		funcCodes := map[string]int{"2": cpInsert, "3": cpDelete, "5": cpPageUp, "6": cpPageDown, "7": cpHome, "8": cpEnd}
		codepoint, ok := funcCodes[m[1]]
		if !ok {
			return parsedKittySequence{}, false
		}
		return parsedKittySequence{
			codepoint: codepoint,
			modifier:  parseIntOr(m[2], 1) - 1,
			eventType: parseEventType(m[3]),
		}, true
	}
	if m := kittyHFRegex.FindStringSubmatch(data); m != nil {
		codepoint := cpEnd
		if m[3] == "H" {
			codepoint = cpHome
		}
		return parsedKittySequence{
			codepoint: codepoint,
			modifier:  parseIntOr(m[1], 1) - 1,
			eventType: parseEventType(m[2]),
		}, true
	}
	return parsedKittySequence{}, false
}

func parseModifyOtherKeysSequence(data string) (parsedModifyOtherKeys, bool) {
	m := modOtherRegex.FindStringSubmatch(data)
	if m == nil {
		return parsedModifyOtherKeys{}, false
	}
	return parsedModifyOtherKeys{
		codepoint: parseIntOr(m[2], 0),
		modifier:  parseIntOr(m[1], 1) - 1,
	}, true
}

func normalizeKittyFunctionalCodepoint(codepoint int) int {
	if equivalent, ok := kittyFunctionalEquivalents[codepoint]; ok {
		return equivalent
	}
	return codepoint
}

func normalizeShiftedLetterIdentity(codepoint, modifier int) int {
	if modifier&^modLock&modShift != 0 && codepoint >= 'A' && codepoint <= 'Z' {
		return codepoint + 32
	}
	return codepoint
}

func matchesKittySequence(data string, expectedCodepoint, expectedModifier int) bool {
	parsed, ok := parseKittySequence(data)
	if !ok {
		return false
	}
	actualMod := parsed.modifier &^ modLock
	expectedMod := expectedModifier &^ modLock
	if actualMod != expectedMod {
		return false
	}
	normalized := normalizeShiftedLetterIdentity(normalizeKittyFunctionalCodepoint(parsed.codepoint), parsed.modifier)
	expectedNormalized := normalizeShiftedLetterIdentity(normalizeKittyFunctionalCodepoint(expectedCodepoint), expectedModifier)
	if normalized == expectedNormalized {
		return true
	}
	if parsed.hasBaseLayout && parsed.baseLayoutKey == normalizeKittyFunctionalCodepoint(expectedCodepoint) {
		if normalized >= 'a' && normalized <= 'z' {
			return false
		}
		if normalized >= 0 && normalized < 128 && symbolKeys[rune(normalized)] {
			return false
		}
		return true
	}
	return false
}

func matchesPrintableModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	if expectedModifier == 0 {
		return false
	}
	parsed, ok := parseModifyOtherKeysSequence(data)
	if !ok || parsed.modifier != expectedModifier {
		return false
	}
	return normalizeShiftedLetterIdentity(parsed.codepoint, parsed.modifier) ==
		normalizeShiftedLetterIdentity(expectedKeycode, expectedModifier)
}

func matchesModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	parsed, ok := parseModifyOtherKeysSequence(data)
	if !ok {
		return false
	}
	return parsed.codepoint == expectedKeycode && parsed.modifier == expectedModifier
}

func rawCtrlChar(key string) (string, bool) {
	char := strings.ToLower(key)
	if char == "" {
		return "", false
	}
	code := rune(char[0])
	if (code >= 'a' && code <= 'z') || char == "[" || char == "\\" || char == "]" || char == "_" {
		return string(code & 0x1f), true
	}
	if char == "-" {
		return string(rune(31)), true
	}
	return "", false
}

type keyIDParts struct {
	key                        string
	ctrl, shift, alt, superMod bool
}

func parseKeyID(keyID string) (keyIDParts, bool) {
	lowered := strings.ToLower(keyID)
	parts := strings.Split(lowered, "+")
	if len(parts) == 0 {
		return keyIDParts{}, false
	}
	parsed := keyIDParts{key: parts[len(parts)-1]}
	if parsed.key == "" {
		return keyIDParts{}, false
	}
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "ctrl":
			parsed.ctrl = true
		case "shift":
			parsed.shift = true
		case "alt":
			parsed.alt = true
		case "super":
			parsed.superMod = true
		default:
			return keyIDParts{}, false
		}
	}
	return parsed, true
}

func (p keyIDParts) modifier() int {
	modifier := 0
	if p.shift {
		modifier |= modShift
	}
	if p.alt {
		modifier |= modAlt
	}
	if p.ctrl {
		modifier |= modCtrl
	}
	if p.superMod {
		modifier |= modSuper
	}
	return modifier
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isDigitKey(key string) bool {
	return len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

func isSingleKey(key string) bool {
	if len(key) != 1 {
		return false
	}
	r := rune(key[0])
	return (r >= 'a' && r <= 'z') || isDigitKey(key) || symbolKeys[r]
}

func matchesKeyModifierSequence(data string, key string, modifier int) bool {
	switch modifier {
	case modShift:
		return legacyShiftSequences[key] == data
	case modCtrl:
		return legacyCtrlSequences[key] == data
	}
	return false
}

func MatchesKey(data string, keyID string) bool {
	parsed, ok := parseKeyID(keyID)
	if !ok {
		return false
	}
	key := parsed.key
	modifier := parsed.modifier()

	switch key {
	case "escape", "esc":
		if modifier != 0 {
			return false
		}
		return data == ANSIESC ||
			matchesKittySequence(data, cpEscape, 0) ||
			matchesModifyOtherKeys(data, cpEscape, 0)

	case "space":
		if !KittyProtocolActive() {
			if modifier == modCtrl && data == "\x00" {
				return true
			}
			if modifier == modAlt && data == "\x1b " {
				return true
			}
		}
		if modifier == 0 {
			return data == " " ||
				matchesKittySequence(data, cpSpace, 0) ||
				matchesModifyOtherKeys(data, cpSpace, 0)
		}
		return matchesKittySequence(data, cpSpace, modifier) ||
			matchesModifyOtherKeys(data, cpSpace, modifier)

	case "tab":
		if modifier == modShift {
			return data == "\x1b[Z" ||
				matchesKittySequence(data, cpTab, modShift) ||
				matchesModifyOtherKeys(data, cpTab, modShift)
		}
		if modifier == 0 {
			return data == "\t" || matchesKittySequence(data, cpTab, 0)
		}
		return matchesKittySequence(data, cpTab, modifier) ||
			matchesModifyOtherKeys(data, cpTab, modifier)

	case "enter", "return":
		return matchesEnterKey(data, modifier)

	case "backspace":
		return matchesBackspaceKey(data, modifier)

	case "insert":
		if modifier == 0 {
			return legacyKeySequences["insert"] == data ||
				matchesKittySequence(data, cpInsert, 0)
		}
		if matchesKeyModifierSequence(data, "insert", modifier) {
			return true
		}
		return matchesKittySequence(data, cpInsert, modifier)

	case "delete":
		if modifier == 0 {
			return legacyKeySequences["delete"] == data ||
				matchesKittySequence(data, cpDelete, 0)
		}
		if matchesKeyModifierSequence(data, "delete", modifier) {
			return true
		}
		return matchesKittySequence(data, cpDelete, modifier)

	case "clear":
		if modifier == 0 {
			return legacyKeySequences["clear"] == data
		}
		return matchesKeyModifierSequence(data, "clear", modifier)

	case "home":
		return matchesFunctionalKey(data, "home", cpHome, modifier)
	case "end":
		return matchesFunctionalKey(data, "end", cpEnd, modifier)
	case "pageup":
		return matchesFunctionalKey(data, "pageUp", cpPageUp, modifier)
	case "pagedown":
		return matchesFunctionalKey(data, "pageDown", cpPageDown, modifier)

	case "up":
		return matchesArrowKey(data, "up", cpArrowUp, modifier)
	case "down":
		return matchesArrowKey(data, "down", cpArrowDown, modifier)
	case "left":
		return matchesLeftRightKey(data, cpArrowLeft, modifier, "\x1bB", "\x1bb")
	case "right":
		return matchesLeftRightKey(data, cpArrowRight, modifier, "\x1bF", "\x1bf")
	}

	if len(key) >= 2 && key[0] == 'f' && isAllDigits(key[1:]) {
		if modifier != 0 {
			return false
		}
		number := 0
		for i := 1; i < len(key); i++ {
			number = number*10 + int(key[i]-'0')
		}
		if number < 1 || number > 12 {
			return false
		}
		sequence, ok := legacyKeySequences[key]
		return ok && sequence == data
	}

	if isSingleKey(key) {
		return matchesSingleKey(data, key, modifier)
	}
	return false
}

func matchesEnterKey(data string, modifier int) bool {
	switch modifier {
	case modShift:
		if matchesKittySequence(data, cpEnter, modShift) ||
			matchesKittySequence(data, cpKpEnter, modShift) ||
			matchesModifyOtherKeys(data, cpEnter, modShift) {
			return true
		}
		if KittyProtocolActive() {
			return data == "\x1b\r" || data == "\n"
		}
		return false
	case modAlt:
		if matchesKittySequence(data, cpEnter, modAlt) ||
			matchesKittySequence(data, cpKpEnter, modAlt) ||
			matchesModifyOtherKeys(data, cpEnter, modAlt) {
			return true
		}
		if !KittyProtocolActive() {
			return data == "\x1b\r"
		}
		return false
	case 0:
		return data == "\r" ||
			(!KittyProtocolActive() && data == "\n") ||
			data == "\x1bOM" ||
			matchesKittySequence(data, cpEnter, 0) ||
			matchesKittySequence(data, cpKpEnter, 0)
	}
	return matchesKittySequence(data, cpEnter, modifier) ||
		matchesKittySequence(data, cpKpEnter, modifier) ||
		matchesModifyOtherKeys(data, cpEnter, modifier)
}

func matchesBackspaceKey(data string, modifier int) bool {
	switch modifier {
	case modAlt:
		if data == "\x1b\x7f" || data == "\x1b\b" {
			return true
		}
		return matchesKittySequence(data, cpBackspace, modAlt) ||
			matchesModifyOtherKeys(data, cpBackspace, modAlt)
	case modCtrl:
		if data == "\x08" {
			return true
		}
		return matchesKittySequence(data, cpBackspace, modCtrl) ||
			matchesModifyOtherKeys(data, cpBackspace, modCtrl)
	case 0:
		if data == "\x7f" || data == "\x08" {
			return true
		}
		return matchesKittySequence(data, cpBackspace, 0) ||
			matchesModifyOtherKeys(data, cpBackspace, 0)
	}
	return matchesKittySequence(data, cpBackspace, modifier) ||
		matchesModifyOtherKeys(data, cpBackspace, modifier)
}

func matchesFunctionalKey(data, legacyName string, codepoint, modifier int) bool {
	if modifier == 0 {
		if legacyKeySequences[legacyName] == data {
			return true
		}
		if alt, ok := legacyAltSequences[legacyName]; ok && alt == data {
			return true
		}
		return matchesKittySequence(data, codepoint, 0)
	}
	if matchesKeyModifierSequence(data, legacyName, modifier) {
		return true
	}
	return matchesKittySequence(data, codepoint, modifier)
}

func matchesArrowKey(data, legacyName string, codepoint, modifier int) bool {
	switch modifier {
	case modAlt:
		return data == legacyAltArrowSequences[legacyName] || matchesKittySequence(data, codepoint, modAlt)
	case 0:
		return legacyKeySequences[legacyName] == data || matchesKittySequence(data, codepoint, 0)
	}
	if matchesKeyModifierSequence(data, legacyName, modifier) {
		return true
	}
	return matchesKittySequence(data, codepoint, modifier)
}

func matchesLeftRightKey(data string, codepoint, modifier int, legacyUpper, legacyLower string) bool {
	finalLetter := arrowFinalLetter(codepoint)
	switch modifier {
	case modAlt:
		if data == "\x1b[1;3"+finalLetter {
			return true
		}
		if !KittyProtocolActive() && data == legacyUpper {
			return true
		}
		return data == legacyLower || matchesKittySequence(data, codepoint, modAlt)
	case modCtrl:
		if data == "\x1b[1;5"+finalLetter {
			return true
		}
		return matchesKeyModifierSequence(data, arrowLegacyName(codepoint), modCtrl) ||
			matchesKittySequence(data, codepoint, modCtrl)
	case 0:
		return legacyKeySequences[arrowLegacyName(codepoint)] == data ||
			matchesKittySequence(data, codepoint, 0)
	}
	if matchesKeyModifierSequence(data, arrowLegacyName(codepoint), modifier) {
		return true
	}
	return matchesKittySequence(data, codepoint, modifier)
}

func arrowFinalLetter(codepoint int) string {
	if codepoint == cpArrowLeft {
		return "D"
	}
	return "C"
}

func arrowLegacyName(codepoint int) string {
	switch codepoint {
	case cpArrowLeft:
		return "left"
	case cpArrowRight:
		return "right"
	case cpArrowUp:
		return "up"
	}
	return "down"
}

func matchesSingleKey(data, key string, modifier int) bool {
	codepoint := int(key[0])
	rawCtrl, hasCtrl := rawCtrlChar(key)
	isLetter := key[0] >= 'a' && key[0] <= 'z'

	if modifier == modCtrl|modAlt && !KittyProtocolActive() && hasCtrl {
		if data == ANSIESC+rawCtrl {
			return true
		}
	}
	if modifier == modAlt && !KittyProtocolActive() {
		if data == ANSIESC+key {
			return true
		}
	}
	if modifier == modCtrl {
		if hasCtrl && data == rawCtrl {
			return true
		}
		return matchesKittySequence(data, codepoint, modCtrl) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modCtrl)
	}
	if modifier == modShift|modCtrl {
		return matchesKittySequence(data, codepoint, modShift|modCtrl) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modShift|modCtrl)
	}
	if modifier == modShift {
		if isLetter && data == strings.ToUpper(key) {
			return true
		}
		return matchesKittySequence(data, codepoint, modShift) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modShift)
	}
	if modifier != 0 {
		return matchesKittySequence(data, codepoint, modifier) ||
			matchesPrintableModifyOtherKeys(data, codepoint, modifier)
	}
	return data == key || matchesKittySequence(data, codepoint, 0)
}

func IsKeyRelease(data string) bool {
	if strings.Contains(data, BracketedPasteStart) {
		return false
	}
	return containsEventSuffix(data, ":3")
}

func IsKeyRepeat(data string) bool {
	if strings.Contains(data, BracketedPasteStart) {
		return false
	}
	return containsEventSuffix(data, ":2")
}

func containsEventSuffix(data, marker string) bool {
	for _, suffix := range []string{"u", "~", "A", "B", "C", "D", "H", "F"} {
		if strings.Contains(data, marker+suffix) {
			return true
		}
	}
	return false
}

func formatKeyNameWithModifiers(keyName string, modifier int) (string, bool) {
	var mods []string
	effective := modifier &^ modLock
	supported := modShift | modCtrl | modAlt | modSuper
	if effective&^supported != 0 {
		return "", false
	}
	if effective&modShift != 0 {
		mods = append(mods, "shift")
	}
	if effective&modCtrl != 0 {
		mods = append(mods, "ctrl")
	}
	if effective&modAlt != 0 {
		mods = append(mods, "alt")
	}
	if effective&modSuper != 0 {
		mods = append(mods, "super")
	}
	if len(mods) == 0 {
		return keyName, true
	}
	return strings.Join(mods, "+") + "+" + keyName, true
}

func formatParsedKey(codepoint, modifier, baseLayoutKey int) (string, bool) {
	normalized := normalizeKittyFunctionalCodepoint(codepoint)
	identity := normalizeShiftedLetterIdentity(normalized, modifier)

	isLatin := identity >= 'a' && identity <= 'z'
	isDigit := identity >= '0' && identity <= '9'
	knownSymbol := identity >= 0 && identity < 128 && symbolKeys[rune(identity)]
	effective := identity
	if !isLatin && !isDigit && !knownSymbol && baseLayoutKey != 0 {
		effective = baseLayoutKey
	}

	var keyName string
	switch {
	case effective == cpEscape:
		keyName = "escape"
	case effective == cpTab:
		keyName = "tab"
	case effective == cpEnter || effective == cpKpEnter:
		keyName = "enter"
	case effective == cpSpace:
		keyName = "space"
	case effective == cpBackspace:
		keyName = "backspace"
	case effective == cpDelete:
		keyName = "delete"
	case effective == cpInsert:
		keyName = "insert"
	case effective == cpHome:
		keyName = "home"
	case effective == cpEnd:
		keyName = "end"
	case effective == cpPageUp:
		keyName = "pageUp"
	case effective == cpPageDown:
		keyName = "pageDown"
	case effective == cpArrowUp:
		keyName = "up"
	case effective == cpArrowDown:
		keyName = "down"
	case effective == cpArrowLeft:
		keyName = "left"
	case effective == cpArrowRight:
		keyName = "right"
	case effective >= '0' && effective <= '9', effective >= 'a' && effective <= 'z':
		keyName = string(rune(effective))
	case effective >= 0 && effective < 128 && symbolKeys[rune(effective)]:
		keyName = string(rune(effective))
	default:
		return "", false
	}
	return formatKeyNameWithModifiers(keyName, modifier)
}

func ParseKey(data string) (string, bool) {
	if kitty, ok := parseKittySequence(data); ok {
		return formatParsedKey(kitty.codepoint, kitty.modifier, kitty.baseLayoutKey)
	}
	if mok, ok := parseModifyOtherKeysSequence(data); ok {
		return formatParsedKey(mok.codepoint, mok.modifier, 0)
	}
	if KittyProtocolActive() {
		if data == "\x1b\r" || data == "\n" || data == "\r" {
			return "shift+enter", true
		}
	}
	if keyID, ok := legacySequenceKeyIDs[data]; ok {
		return keyID, true
	}

	switch data {
	case ANSIESC:
		return "escape", true
	case "\x1c":
		return "ctrl+\\", true
	case "\x1d":
		return "ctrl+]", true
	case "\x1f":
		return "ctrl+-", true
	case "\x1b\x1b":
		return "ctrl+alt+[", true
	case "\x1b\x1c":
		return "ctrl+alt+\\", true
	case "\x1b\x1d":
		return "ctrl+alt+]", true
	case "\x1b\x1f":
		return "ctrl+alt+-", true
	case "\t":
		return "tab", true
	case "\r":
		return "enter", true
	case "\n":
		if !KittyProtocolActive() {
			return "enter", true
		}
	case "\x1bOM":
		return "enter", true
	case "\x00":
		return "ctrl+space", true
	case " ":
		return "space", true
	case "\x7f", "\x08":
		return "backspace", true
	case "\x1b[Z":
		return "shift+tab", true
	case "\x1b\r":
		if !KittyProtocolActive() {
			return "alt+enter", true
		}
	case "\x1b ":
		if !KittyProtocolActive() {
			return "alt+space", true
		}
	case "\x1b\x7f", "\x1b\b":
		return "alt+backspace", true
	case "\x1bB":
		if !KittyProtocolActive() {
			return "alt+left", true
		}
	case "\x1bF":
		if !KittyProtocolActive() {
			return "alt+right", true
		}
	case "\x1b[A":
		return "up", true
	case "\x1b[B":
		return "down", true
	case "\x1b[C":
		return "right", true
	case "\x1b[D":
		return "left", true
	case "\x1b[H", "\x1bOH":
		return "home", true
	case "\x1b[F", "\x1bOF":
		return "end", true
	case "\x1b[3~":
		return "delete", true
	case "\x1b[5~":
		return "pageUp", true
	case "\x1b[6~":
		return "pageDown", true
	}

	if !KittyProtocolActive() && len(data) == 2 && data[0] == ANSIESC[0] {
		code := int(data[1])
		if code >= 1 && code <= 26 {
			return "ctrl+alt+" + string(rune(code+96)), true
		}
		key := rune(code)
		if (code >= 'a' && code <= 'z') || (code >= '0' && code <= '9') || symbolKeys[key] {
			return "alt+" + string(key), true
		}
	}

	if len(data) == 1 {
		code := int(data[0])
		if code >= 1 && code <= 26 {
			return "ctrl+" + string(rune(code+96)), true
		}
		if code >= 32 && code <= 126 {
			return data, true
		}
	}
	return "", false
}

var kittyPrintableRegex = kittyCSIURegex

func DecodePrintableKey(data string) (string, bool) {
	if char, ok := decodeKittyPrintable(data); ok {
		return char, true
	}
	return decodeModifyOtherKeysPrintable(data)
}

func decodeKittyPrintable(data string) (string, bool) {
	m := kittyPrintableRegex.FindStringSubmatch(data)
	if m == nil {
		return "", false
	}
	codepoint := parseIntOr(m[1], -1)
	modifier := parseIntOr(m[4], 1) - 1
	allowed := modShift | modLock
	if modifier&^allowed != 0 || modifier&(modAlt|modCtrl) != 0 {
		return "", false
	}
	effective := codepoint
	if modifier&modShift != 0 && m[2] != "" {
		effective = parseIntOr(m[2], -1)
	}
	effective = normalizeKittyFunctionalCodepoint(effective)
	if effective < 32 {
		return "", false
	}
	return string(rune(effective)), true
}

func decodeModifyOtherKeysPrintable(data string) (string, bool) {
	parsed, ok := parseModifyOtherKeysSequence(data)
	if !ok {
		return "", false
	}
	if (parsed.modifier&^modLock)&^modShift != 0 {
		return "", false
	}
	if parsed.codepoint < 32 {
		return "", false
	}
	return string(rune(parsed.codepoint)), true
}
