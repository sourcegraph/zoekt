//go:build !windows && !wasm

package index

import (
	"os"

	"golang.org/x/sys/unix"
)

func init() {
	umask = os.FileMode(unix.Umask(0))
	unix.Umask(int(umask))
}
