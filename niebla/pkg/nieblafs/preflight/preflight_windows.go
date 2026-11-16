//go:build windows

package preflight

import "os"

func isFileRootOwned(info os.FileInfo) bool {
	return false
}
