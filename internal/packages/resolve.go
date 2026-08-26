package packages

import (
	"cmp"
	"strconv"
	"strings"
)

type canonicalVersion struct {
	major int
	minor int
	patch int
}

func isCanonicalVersion(s string) bool {
	_, ok := parseCanonicalVersion(s)
	return ok
}

func parseCanonicalVersion(s string) (canonicalVersion, bool) {
	if len(s) < 2 || s[0] != 'v' {
		return canonicalVersion{}, false
	}
	parts := strings.Split(s[1:], ".")
	if len(parts) != 3 {
		return canonicalVersion{}, false
	}
	var v canonicalVersion
	for i, p := range parts {
		if !allDigits(p) || (len(p) > 1 && p[0] == '0') {
			return canonicalVersion{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return canonicalVersion{}, false
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	return v, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareVersions(a, b string) int {
	va, oka := parseCanonicalVersion(a)
	vb, okb := parseCanonicalVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	switch {
	case va.major != vb.major:
		return cmp.Compare(va.major, vb.major)
	case va.minor != vb.minor:
		return cmp.Compare(va.minor, vb.minor)
	default:
		return cmp.Compare(va.patch, vb.patch)
	}
}
