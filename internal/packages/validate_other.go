//go:build !unix

package packages

import "os"

func checkNoHardlinks(string, os.FileInfo) error {
	return nil
}
