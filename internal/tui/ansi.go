package tui

const (
	ANSIESC = "\x1b"
	ANSIBEL = "\x07"
	ANSIST  = "\x1b\\"
)

const (
	SGRReset     = "\x1b[0m"
	SGRBold      = "\x1b[1m"
	SGRDim       = "\x1b[2m"
	SGRItalic    = "\x1b[3m"
	SGRUnderline = "\x1b[4m"
	SGRBlink     = "\x1b[5m"
	SGRInverse   = "\x1b[7m"
	SGRHidden    = "\x1b[8m"
	SGRStrike    = "\x1b[9m"

	SGRBoldOff      = "\x1b[22m"
	SGRItalicOff    = "\x1b[23m"
	SGRUnderlineOff = "\x1b[24m"
	SGRBlinkOff     = "\x1b[25m"
	SGRInverseOff   = "\x1b[27m"
	SGRStrikeOff    = "\x1b[29m"

	SGRFgDefault = "\x1b[39m"
	SGRBgDefault = "\x1b[49m"
)

const (
	CursorUp             = "\x1b[A"
	CursorDown           = "\x1b[B"
	CursorRight          = "\x1b[C"
	CursorLeft           = "\x1b[D"
	CursorHide           = "\x1b[?25l"
	CursorShow           = "\x1b[?25h"
	CursorEraseLine      = "\x1b[2K"
	CursorEraseBelow     = "\x1b[J"
	CursorEraseScreen    = "\x1b[2J"
	CursorHome           = "\x1b[H"
	CursorClearScroll    = "\x1b[3J"
	CursorReportPos      = "\x1b[6n"
	CursorDeviceAttrs    = "\x1b[c"
	SyncOutputBegin      = "\x1b[?2026h"
	SyncOutputEnd        = "\x1b[?2026l"
	AltScreenEnter       = "\x1b[?1049h"
	AltScreenExit        = "\x1b[?1049l"
	AutowrapDisable      = "\x1b[?7l"
	AutowrapEnable       = "\x1b[?7h"
	BracketedPasteOn     = "\x1b[?2004h"
	BracketedPasteOff    = "\x1b[?2004l"
	FocusEventsOn        = "\x1b[?1004h"
	FocusEventsOff       = "\x1b[?1004l"
	MouseNormalOn        = "\x1b[?1000h"
	MouseButtonMotionOn  = "\x1b[?1002h"
	MouseAllMotionOn     = "\x1b[?1003h"
	MouseSGROn           = "\x1b[?1006h"
	MouseSGROff          = "\x1b[?1006l"
	MouseAllMotionOff    = "\x1b[?1003l"
	MouseButtonMotionOff = "\x1b[?1002l"
	MouseNormalOff       = "\x1b[?1000l"
	ColorSchemeQuery     = "\x1b[?996n"
	ColorSchemeNotifyOn  = "\x1b[?2031h"
	ColorSchemeNotifyOff = "\x1b[?2031l"
)

const (
	OSCTitlePrefix     = "\x1b]0;"
	OSCQueryBackground = "\x1b]11;?\x07"
	OSC8Close          = "\x1b]8;;\x07"
	OSC8CloseST        = "\x1b]8;;\x1b\\"
)

const (
	OSC133PromptStart = "\x1b]133;A\x07"
	OSC133CommandEnd  = "\x1b]133;B\x07"
	OSC133OutputEnd   = "\x1b]133;C\x07"
)

const (
	ModifyOtherKeysEnable  = "\x1b[>4;2m"
	ModifyOtherKeysDisable = "\x1b[>4;0m"
	KittyProtocolPop       = "\x1b[<u"
)

const KittyQueryFlags = 7

func SGRFg256(index int) string {
	return "\x1b[38;5;" + itoa(index) + "m"
}

func SGRBg256(index int) string {
	return "\x1b[48;5;" + itoa(index) + "m"
}

func SGRFgRGB(r, g, b int) string {
	return "\x1b[38;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b) + "m"
}

func SGRBgRGB(r, g, b int) string {
	return "\x1b[48;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b) + "m"
}

func CursorTo(row, col int) string {
	return "\x1b[" + itoa(row+1) + ";" + itoa(col+1) + "H"
}

func CursorColumn(col int) string {
	return "\x1b[" + itoa(col+1) + "G"
}

func CursorMoveLines(lines int) string {
	if lines > 0 {
		return "\x1b[" + itoa(lines) + "B"
	}
	if lines < 0 {
		return "\x1b[" + itoa(-lines) + "A"
	}
	return ""
}

func MouseEnable(buttonMotionOnly bool) string {
	seq := MouseNormalOn + MouseButtonMotionOn + FocusEventsOn + MouseSGROn
	if !buttonMotionOnly {
		seq = MouseNormalOn + MouseButtonMotionOn + MouseAllMotionOn + FocusEventsOn + MouseSGROn
	}
	return seq
}

func MouseDisable() string {
	return MouseSGROff + FocusEventsOff + MouseAllMotionOff + MouseButtonMotionOff + MouseNormalOff
}

func OSCTitle(title string) string {
	return OSCTitlePrefix + title + ANSIBEL
}

func OSC52Clipboard(base64Text string) string {
	return "\x1b]52;c;" + base64Text + ANSIBEL
}

func OSC8Hyperlink(params, url string) string {
	return "\x1b]8;" + params + ";" + url + ANSIBEL
}

func BracketedPaste(content string) string {
	return "\x1b[200~" + content + "\x1b[201~"
}

const BracketedPasteStart = "\x1b[200~"

const BracketedPasteEnd = "\x1b[201~"

const CursorMarker = "\x1b_pi:c\x07"

const SegmentReset = SGRReset + OSC8Close

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits [20]byte
	pos := len(digits)
	for v > 0 {
		pos--
		digits[pos] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}

const (
	FocusIn  = "\x1b[I"
	FocusOut = "\x1b[O"
)
