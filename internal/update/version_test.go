package update

import (
	"errors"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.3", "v1.2.2", 1},
		{"v1.10.0", "v1.9.9", 1},
		{"1.2.3", "v1.2.3", 0},
		{"V1.2.3", "v1.2.3", 0},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.2", "v1.2.0", 0},
		{"v1.2.3-beta.1", "v1.2.3", 0},
		{"v1.2.3", "dev", 1},
		{"dev", "dev", 0},
		{"", "", 0},
		{"v1.2.3", "", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d (antisymmetry)", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestFindChecksum(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	other := strings.Repeat("cd", 32)
	cases := []struct {
		name      string
		content   string
		want      string
		wantError bool
	}{
		{"single entry", digest + "  smidja-linux-amd64\n", digest, false},
		{"BSD star prefix", digest + " *smidja-linux-amd64\n", digest, false},
		{"uppercase digest", strings.ToUpper(digest) + "  smidja-linux-amd64\n", digest, false},
		{"extra fields after name", digest + "  smidja-linux-amd64  trailing\n", digest, false},
		{"crlf line ending", digest + "  smidja-linux-amd64\r\n", digest, false},
		{"unrelated entries only", other + "  smidja-linux-arm64\n", "", true},
		{"malformed digest for asset", "xyz  smidja-linux-amd64\n", "", true},
		{"wrong length digest", "abcd  smidja-linux-amd64\n", "", true},
		{"duplicate entries", digest + "  smidja-linux-amd64\n" + digest + "  smidja-linux-amd64\n", "", true},
		{"empty file", "", "", true},
		{"garbage only", "this is not a checksum file\n", "", true},
	}
	for _, tc := range cases {
		got, err := findChecksum([]byte(tc.content), "smidja-linux-amd64")
		if tc.wantError {
			if err == nil || !errors.Is(err, ErrChecksumEntry) {
				t.Errorf("%s: err = %v, want ErrChecksumEntry", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
