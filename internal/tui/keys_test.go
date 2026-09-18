package tui

import "testing"

func TestMatchesKeyLegacy(t *testing.T) {
	SetKittyProtocolActive(false)
	defer SetKittyProtocolActive(false)

	tests := []struct {
		data  string
		keyID string
		want  bool
	}{
		{"\x1b[A", "up", true},
		{"\x1b[B", "down", true},
		{"\x1b[C", "right", true},
		{"\x1b[D", "left", true},
		{"\x1b[H", "home", true},
		{"\x1b[1~", "home", true},
		{"\x1b[4~", "end", true},
		{"\x1b[2~", "insert", true},
		{"\x1b[3~", "delete", true},
		{"\x1b[5~", "pageUp", true},
		{"\x1b[6~", "pageDown", true},
		{"\x1bOP", "f1", true},
		{"\x1b[15~", "f5", true},
		{"\x1b[24~", "f12", true},
		{"\x1b[Z", "shift+tab", true},
		{"\t", "tab", true},
		{"\r", "enter", true},
		{"\n", "enter", true},
		{"\x1bOM", "enter", true},
		{"\x7f", "backspace", true},
		{"\x08", "backspace", true},
		{"\x08", "ctrl+backspace", true},
		{"\x1b\x7f", "alt+backspace", true},
		{"\x00", "ctrl+space", true},
		{" ", "space", true},
		{"\x1b ", "alt+space", true},
		{"\x1b", "escape", true},
		{"a", "a", true},
		{"A", "shift+a", true},
		{"\x01", "ctrl+a", true},
		{"\x1b\x01", "ctrl+alt+a", true},
		{"\x1ba", "alt+a", true},
		{"\x1b1", "alt+1", true},
		{"\x1bb", "alt+left", true},
		{"\x1bB", "alt+left", true},
		{"\x1b[1;3D", "alt+left", true},
		{"\x1b[1;5D", "ctrl+left", true},
		{"\x1b[1;5C", "ctrl+right", true},
		{"\x1bOa", "ctrl+up", true},
		{"\x1b[a", "shift+up", true},
		{"\x1b[5^", "ctrl+pageUp", true},
		{"\x1b[6$", "shift+pageDown", true},
		{"\x1f", "ctrl+-", true},
		{"\x1c", "ctrl+\\", true},
		{"\x1d", "ctrl+]", true},
		{"\x1b\x1b", "ctrl+alt+[", true},
		{"\x1b\r", "alt+enter", true},
		{"x", "a", false},
		{"\x1b[A", "down", false},
		{"\x01", "ctrl+b", false},
	}
	for _, test := range tests {
		if got := MatchesKey(test.data, test.keyID); got != test.want {
			t.Errorf("MatchesKey(%q, %q) = %v, want %v", test.data, test.keyID, got, test.want)
		}
	}
}

func TestMatchesKeyKitty(t *testing.T) {
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)

	tests := []struct {
		data  string
		keyID string
		want  bool
	}{
		{"\x1b[97u", "a", true},
		{"\x1b[97;1:1u", "a", true},
		{"\x1b[97;2u", "shift+a", true},
		{"\x1b[65;2u", "shift+a", true},
		{"\x1b[97;5u", "ctrl+a", true},
		{"\x1b[97;3u", "alt+a", true},
		{"\x1b[97;7u", "ctrl+alt+a", true},
		{"\x1b[57414u", "enter", true},
		{"\x1b[13u", "enter", true},
		{"\x1b[13;2u", "shift+enter", true},
		{"\x1b[9;2u", "shift+tab", true},
		{"\x1b[9u", "tab", true},
		{"\x1b[127u", "backspace", true},
		{"\x1b[127;3u", "alt+backspace", true},
		{"\x1b[32u", "space", true},
		{"\x1b[27u", "escape", true},
		{"\x1b[57399u", "0", true},
		{"\x1b[57417u", "left", true},
		{"\x1b[57419u", "up", true},
		{"\x1b[1;5D", "ctrl+left", true},
		{"\x1b[3;5~", "ctrl+delete", true},
		{"\x1b[1;2H", "shift+home", true},
		{"\x1b[97;1:3u", "a", true},
		{"\x1b[97u", "b", false},
		{"\x1b[97;5u", "shift+a", false},
		{"\x1b[65;2u", "shift+b", false},
	}
	for _, test := range tests {
		if got := MatchesKey(test.data, test.keyID); got != test.want {
			t.Errorf("MatchesKey(%q, %q) = %v, want %v", test.data, test.keyID, got, test.want)
		}
	}
}

func TestMatchesKeyModifyOtherKeys(t *testing.T) {
	SetKittyProtocolActive(false)
	defer SetKittyProtocolActive(false)

	tests := []struct {
		data  string
		keyID string
		want  bool
	}{
		{"\x1b[27;5;97~", "ctrl+a", true},
		{"\x1b[27;2;65~", "shift+a", true},
		{"\x1b[27;7;97~", "ctrl+alt+a", true},
		{"\x1b[27;5;13~", "ctrl+enter", true},
		{"\x1b[27;5;97~", "ctrl+b", false},
		{"\x1b[27;5;97~", "a", false},
	}
	for _, test := range tests {
		if got := MatchesKey(test.data, test.keyID); got != test.want {
			t.Errorf("MatchesKey(%q, %q) = %v, want %v", test.data, test.keyID, got, test.want)
		}
	}
}

func TestParseKeyLegacy(t *testing.T) {
	SetKittyProtocolActive(false)
	defer SetKittyProtocolActive(false)

	tests := []struct {
		data   string
		wantID string
		wantOK bool
	}{
		{"\x1b[A", "up", true},
		{"\x1b[H", "home", true},
		{"\x1bOH", "home", true},
		{"\x1b[1~", "home", true},
		{"\x1b[7~", "home", true},
		{"\x1b[4~", "end", true},
		{"\x1b[2~", "insert", true},
		{"\x1b[3~", "delete", true},
		{"\x1b[3$", "shift+delete", true},
		{"\x1b[3^", "ctrl+delete", true},
		{"\x1b[5~", "pageUp", true},
		{"\x1b[6~", "pageDown", true},
		{"\x1b[[5~", "pageUp", true},
		{"\x1bOP", "f1", true},
		{"\x1b[11~", "f1", true},
		{"\x1b[24~", "f12", true},
		{"\x1b[E", "clear", true},
		{"\x1b[e", "shift+clear", true},
		{"\x1b[a", "shift+up", true},
		{"\x1bOa", "ctrl+up", true},
		{"\x1b[7$", "shift+home", true},
		{"\x1b[8^", "ctrl+end", true},
		{"\x1bb", "alt+left", true},
		{"\x1bf", "alt+right", true},
		{"\x1bp", "alt+up", true},
		{"\x1bn", "alt+down", true},
		{"\x1b", "escape", true},
		{"\x1c", "ctrl+\\", true},
		{"\x1d", "ctrl+]", true},
		{"\x1f", "ctrl+-", true},
		{"\x1b\x1b", "ctrl+alt+[", true},
		{"\x1b\x1c", "ctrl+alt+\\", true},
		{"\x1b\x1d", "ctrl+alt+]", true},
		{"\x1b\x1f", "ctrl+alt+-", true},
		{"\t", "tab", true},
		{"\r", "enter", true},
		{"\n", "enter", true},
		{"\x1bOM", "enter", true},
		{"\x00", "ctrl+space", true},
		{" ", "space", true},
		{"\x7f", "backspace", true},
		{"\x08", "backspace", true},
		{"\x1b[Z", "shift+tab", true},
		{"\x1b\r", "alt+enter", true},
		{"\x1b ", "alt+space", true},
		{"\x1b\x7f", "alt+backspace", true},
		{"\x1bB", "alt+left", true},
		{"\x1bF", "alt+right", true},
		{"\x1b[A", "up", true},
		{"\x1b[B", "down", true},
		{"\x1b[C", "right", true},
		{"\x1b[D", "left", true},
		{"\x01", "ctrl+a", true},
		{"\x1a", "ctrl+z", true},
		{"\x1b\x12", "ctrl+alt+r", true},
		{"\x1bq", "alt+q", true},
		{"\x1b1", "alt+1", true},
		{"\x1b@", "alt+@", true},
		{"a", "a", true},
		{"0", "0", true},
		{"!", "!", true},
		{"", "", false},
		{"\x1b[999x", "", false},
	}
	for _, test := range tests {
		id, ok := ParseKey(test.data)
		if ok != test.wantOK || (ok && id != test.wantID) {
			t.Errorf("ParseKey(%q) = (%q, %v), want (%q, %v)", test.data, id, ok, test.wantID, test.wantOK)
		}
	}
}

func TestParseKeyKitty(t *testing.T) {
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)

	tests := []struct {
		data   string
		wantID string
		wantOK bool
	}{
		{"\x1b[13u", "enter", true},
		{"\x1b[57414u", "enter", true},
		{"\x1b[27u", "escape", true},
		{"\x1b[9u", "tab", true},
		{"\x1b[127u", "backspace", true},
		{"\x1b[32u", "space", true},
		{"\x1b[57399u", "0", true},
		{"\x1b[57412u", "-", true},
		{"\x1b[97;5u", "ctrl+a", true},
		{"\x1b[65;2u", "shift+a", true},
		{"\x1b[57417;5u", "ctrl+left", true},
		{"\x1b[97;1:3u", "a", true},
	}
	for _, test := range tests {
		id, ok := ParseKey(test.data)
		if ok != test.wantOK || (ok && id != test.wantID) {
			t.Errorf("ParseKey(%q) = (%q, %v), want (%q, %v)", test.data, id, ok, test.wantID, test.wantOK)
		}
	}

	if id, ok := ParseKey("\r"); !ok || id != "shift+enter" {
		t.Errorf("ParseKey(\\r) with kitty = (%q, %v), want (shift+enter, true)", id, ok)
	}
}

func TestParseKeyKittyModifiers(t *testing.T) {
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)

	id, ok := ParseKey("\x1b[65;2u")
	if !ok || id != "shift+a" {
		t.Fatalf("ParseKey shifted letter = (%q, %v)", id, ok)
	}
}

func TestDecodePrintableKey(t *testing.T) {
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)

	tests := []struct {
		data  string
		want  string
		wantK bool
	}{
		{"\x1b[97u", "a", true},
		{"\x1b[65;2u", "A", true},
		{"\x1b[946u", "β", true},
		{"\x1b[97;5u", "", false},
		{"\x1b[13u", "", false},
		{"\x1b[27;5;97~", "", false},
	}
	for _, test := range tests {
		char, ok := DecodePrintableKey(test.data)
		if ok != test.wantK || (ok && char != test.want) {
			t.Errorf("DecodePrintableKey(%q) = (%q, %v), want (%q, %v)", test.data, char, ok, test.want, test.wantK)
		}
	}

	SetKittyProtocolActive(false)
	char, ok := DecodePrintableKey("\x1b[27;2;97~")
	if !ok || char != "a" {
		t.Fatalf("DecodePrintableKey modifyOtherKeys shift = (%q, %v), want (a, true)", char, ok)
	}
	char, ok = DecodePrintableKey("\x1b[27;5;97~")
	if ok {
		t.Fatalf("DecodePrintableKey ctrl modifier = (%q, %v), want failure", char, ok)
	}
	char, ok = DecodePrintableKey("\x1b[27;2;13~")
	if ok {
		t.Fatalf("DecodePrintableKey control code = (%q, %v), want failure", char, ok)
	}
}

func TestKeyReleaseAndRepeat(t *testing.T) {
	if IsKeyRelease("\x1b[97;1:3u") != true {
		t.Fatal("IsKeyRelease should detect :3u")
	}
	if IsKeyRelease("\x1b[1;1:3A") != true {
		t.Fatal("IsKeyRelease should detect :3A")
	}
	if IsKeyRelease("a") != false {
		t.Fatal("IsKeyRelease should be false for plain text")
	}
	if IsKeyRelease(BracketedPaste("x")) != false {
		t.Fatal("IsKeyRelease should be false inside paste")
	}
	if IsKeyRepeat("\x1b[97;1:2u") != true {
		t.Fatal("IsKeyRepeat should detect :2u")
	}
	if IsKeyRepeat("\x1b[97;1:3u") != false {
		t.Fatal("IsKeyRepeat should be false for release")
	}
}

func TestMatchesKeyWithLockModifier(t *testing.T) {
	SetKittyProtocolActive(true)
	defer SetKittyProtocolActive(false)
	if !MatchesKey("\x1b[97;65u", "a") {
		t.Fatal("capsLock modifier (bit 64) should be ignored")
	}
}

func TestRawCtrlChar(t *testing.T) {
	tests := []struct {
		key   string
		want  string
		wantK bool
	}{
		{"a", "\x01", true},
		{"A", "\x01", true},
		{"[", "\x1b", true},
		{"\\", "\x1c", true},
		{"]", "\x1d", true},
		{"_", "\x1f", true},
		{"-", "\x1f", true},
		{"1", "", false},
		{"", "", false},
	}
	for _, test := range tests {
		got, ok := rawCtrlChar(test.key)
		if ok != test.wantK || (ok && got != test.want) {
			t.Errorf("rawCtrlChar(%q) = (%q, %v), want (%q, %v)", test.key, got, ok, test.want, test.wantK)
		}
	}
}
