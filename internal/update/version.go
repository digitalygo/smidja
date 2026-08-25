package update

import (
	"strconv"
	"strings"
)

// CompareVersions compares two version strings numerically: a leading "v"
// or "V" and any prerelease suffix after the first "-" are ignored, and
// numeric dot fields are compared left to right with missing fields
// treated as zero. Non-numeric fields are treated as zero. It returns -1,
// 0, or 1 as a is older, equal to, or newer than b. Check uses it to
// decide whether the latest release is an available update.
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

// versionFields converts a version string into its numeric dot fields.
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
			n = 0 // non-numeric field, treated as zero
		}
		fields[i] = n
	}
	return fields
}

// fieldAt returns the i-th field, or 0 when the field does not exist.
func fieldAt(f []int, i int) int {
	if i < len(f) {
		return f[i]
	}
	return 0
}
