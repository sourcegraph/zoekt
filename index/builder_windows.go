//go:build windows

package index

func init() {
	umask = 0 // Windows does not use Unix file permission masks
}
