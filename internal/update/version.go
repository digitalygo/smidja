package update

import (
	"strconv"
	"strings"
)

func CompareVersions(a, b string) int {
	fa, fb := versionFields(a), versionFields(b)
	for i := 0; i < max(len(fa), len(fb)); i++ {
		na, nb := fieldAt(fa, i), fieldAt(fb, i)
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
	}
	return 0
}

func versionFields(v string) []int {
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	fields := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		fields[i] = n
	}
	return fields
}

func fieldAt(f []int, i int) int {
	if i < len(f) {
		return f[i]
	}
	return 0
}
