//go:build !linux

package sessionimport

func commitAtomic(tmpName, destPath string, wantHash []byte) (bool, error) {
	return false, ErrUnsupportedPlatform
}
