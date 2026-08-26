//go:build unix

package packages

import (
	"fmt"
	"os"
	"syscall"
)

func checkNoHardlinks(path string, info os.FileInfo) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return fmt.Errorf("packages: validate: hardlink %s", path)
	}
	return nil
}
