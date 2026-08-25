package munge

import "testing"

func TestMungeCWD(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a//b", "a-b"},
		{"a/b", "a-b"},
		{"/var/home/luca", "-var-home-luca"},
		{"a/b/", "a-b"},
		{"single", "single"},
		{"", ""},
	}
	for _, c := range cases {
		if got := MungeCWD(c.in); got != c.want {
			t.Errorf("MungeCWD(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
