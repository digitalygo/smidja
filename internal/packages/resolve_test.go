package packages

import "testing"

func TestIsCanonicalVersion(t *testing.T) {
	valid := []string{"v0.0.0", "v1.2.3", "v10.20.30", "v0.1.0"}
	for _, v := range valid {
		if !isCanonicalVersion(v) {
			t.Errorf("isCanonicalVersion(%q) = false, want true", v)
		}
	}
	invalid := []string{
		"", "v", "v1", "v1.2", "1.2.3", "V1.2.3", "v01.2.3", "v1.02.3",
		"v1.2.03", "v1.2.3.4", "v1.2.3-beta", "v1.2.x", "1.2", "v..", "v1..3",
	}
	for _, v := range invalid {
		if isCanonicalVersion(v) {
			t.Errorf("isCanonicalVersion(%q) = true, want false", v)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.3", "v1.2.2", 1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v0.0.1", "v0.0.0", 1},
		{"v0.1.0", "v0.0.9", 1},
		{"v1.2.3", "dev", 1},
		{"dev", "v1.2.3", -1},
		{"dev", "dev", 0},
		{"v1.2.3", "", 1},
		{"", "", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := compareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}
