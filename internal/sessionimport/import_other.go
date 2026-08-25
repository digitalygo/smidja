//go:build !linux

package sessionimport

// commitAtomic is unsupported on this platform. The atomic no-replace
// semantics of link(2) are Linux-specific, and a rename-based fallback
// would reintroduce the check-then-replace race this function exists to
// close. Import fails with ErrUnsupportedPlatform and the destination is
// left untouched.
func commitAtomic(tmpName, destPath string, wantHash []byte) (bool, error) {
	return false, ErrUnsupportedPlatform
}
