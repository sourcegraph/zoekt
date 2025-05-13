package workspaces

import (
	"os"
)

// Enabled returns true if the WORKSPACES_API_URL environment variable is set.
// This indicates that workspaces disk layout should be used.
func Enabled() bool {
	return os.Getenv("WORKSPACES_API_URL") != ""
}