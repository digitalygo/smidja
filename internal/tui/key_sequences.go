package tui

type sequenceTable map[string]string

var legacyKeySequences = sequenceTable{
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"home": "\x1b[H", "end": "\x1b[F",
	"insert": "\x1b[2~", "delete": "\x1b[3~",
	"pageUp": "\x1b[5~", "pageDown": "\x1b[6~",
	"clear": "\x1b[E",
	"f1":    "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

var legacyAltSequences = sequenceTable{
	"home": "\x1b[1~", "end": "\x1b[4~",
	"pageUp": "\x1b[[5~", "pageDown": "\x1b[[6~",
	"f1": "\x1b[11~", "f2": "\x1b[12~", "f3": "\x1b[13~", "f4": "\x1b[14~",
	"f5": "\x1b[[E",
}

var legacyShiftSequences = sequenceTable{
	"up": "\x1b[a", "down": "\x1b[b", "right": "\x1b[c", "left": "\x1b[d",
	"clear": "\x1b[e", "insert": "\x1b[2$", "delete": "\x1b[3$",
	"pageUp": "\x1b[5$", "pageDown": "\x1b[6$", "home": "\x1b[7$", "end": "\x1b[8$",
}

var legacyCtrlSequences = sequenceTable{
	"up": "\x1bOa", "down": "\x1bOb", "right": "\x1bOc", "left": "\x1bOd",
	"clear": "\x1bOe", "insert": "\x1b[2^", "delete": "\x1b[3^",
	"pageUp": "\x1b[5^", "pageDown": "\x1b[6^", "home": "\x1b[7^", "end": "\x1b[8^",
}

var legacyAltArrowSequences = sequenceTable{
	"up": "\x1bp", "down": "\x1bn", "left": "\x1bb", "right": "\x1bf",
}

var legacySequenceKeyIDs = map[string]string{
	"\x1bOA": "up", "\x1bOB": "down", "\x1bOC": "right", "\x1bOD": "left",
	"\x1bOH": "home", "\x1bOF": "end",
	"\x1b[E": "clear", "\x1bOE": "clear",
	"\x1bOe": "ctrl+clear", "\x1b[e": "shift+clear",
	"\x1b[2~": "insert", "\x1b[2$": "shift+insert", "\x1b[2^": "ctrl+insert",
	"\x1b[3$": "shift+delete", "\x1b[3^": "ctrl+delete",
	"\x1b[1~": "home", "\x1b[4~": "end",
	"\x1b[7~": "home", "\x1b[8~": "end",
	"\x1b[[5~": "pageUp", "\x1b[[6~": "pageDown",
	"\x1b[a": "shift+up", "\x1b[b": "shift+down", "\x1b[c": "shift+right", "\x1b[d": "shift+left",
	"\x1bOa": "ctrl+up", "\x1bOb": "ctrl+down", "\x1bOc": "ctrl+right", "\x1bOd": "ctrl+left",
	"\x1b[5$": "shift+pageUp", "\x1b[6$": "shift+pageDown",
	"\x1b[7$": "shift+home", "\x1b[8$": "shift+end",
	"\x1b[5^": "ctrl+pageUp", "\x1b[6^": "ctrl+pageDown",
	"\x1b[7^": "ctrl+home", "\x1b[8^": "ctrl+end",
	"\x1bOP": "f1", "\x1bOQ": "f2", "\x1bOR": "f3", "\x1bOS": "f4",
	"\x1b[11~": "f1", "\x1b[12~": "f2", "\x1b[13~": "f3", "\x1b[14~": "f4",
	"\x1b[[A": "f1", "\x1b[[B": "f2", "\x1b[[C": "f3", "\x1b[[D": "f4", "\x1b[[E": "f5",
	"\x1b[15~": "f5", "\x1b[17~": "f6", "\x1b[18~": "f7", "\x1b[19~": "f8",
	"\x1b[20~": "f9", "\x1b[21~": "f10", "\x1b[23~": "f11", "\x1b[24~": "f12",
	"\x1bb": "alt+left", "\x1bf": "alt+right", "\x1bp": "alt+up", "\x1bn": "alt+down",
}

var symbolKeys = map[rune]bool{
	'`': true, '-': true, '=': true, '[': true, ']': true, '\\': true,
	';': true, '\'': true, ',': true, '.': true, '/': true, '!': true,
	'@': true, '#': true, '$': true, '%': true, '^': true, '&': true,
	'*': true, '(': true, ')': true, '_': true, '+': true, '|': true,
	'~': true, '{': true, '}': true, ':': true, '<': true, '>': true, '?': true,
}

const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8
	modLock  = modShift << 6
)

const (
	cpEscape    = 27
	cpTab       = 9
	cpEnter     = 13
	cpSpace     = 32
	cpBackspace = 127
	cpKpEnter   = 57414
)

const (
	cpArrowUp    = -1
	cpArrowDown  = -2
	cpArrowRight = -3
	cpArrowLeft  = -4
)

const (
	cpDelete   = -10
	cpInsert   = -11
	cpPageUp   = -12
	cpPageDown = -13
	cpHome     = -14
	cpEnd      = -15
)

var kittyFunctionalEquivalents = map[int]int{
	57399: '0', 57400: '1', 57401: '2', 57402: '3', 57403: '4',
	57404: '5', 57405: '6', 57406: '7', 57407: '8', 57408: '9',
	57409: '.', 57410: '/', 57411: '*', 57412: '-', 57413: '+',
	57415: '=', 57416: ',',
	57417: cpArrowLeft, 57418: cpArrowRight, 57419: cpArrowUp, 57420: cpArrowDown,
	57421: cpPageUp, 57422: cpPageDown, 57423: cpHome, 57424: cpEnd,
	57425: cpInsert, 57426: cpDelete,
}
