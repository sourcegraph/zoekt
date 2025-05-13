package workspaces

import (
	"os"
	"testing"
)

func TestEnabled(t *testing.T) {
	// Save the original environment variable value
	original := os.Getenv("WORKSPACES_API_URL")
	// Restore the original value when the test completes
	defer os.Setenv("WORKSPACES_API_URL", original)

	// Test when the environment variable is not set
	os.Unsetenv("WORKSPACES_API_URL")
	if Enabled() {
		t.Error("Expected Enabled() to return false when WORKSPACES_API_URL is not set")
	}

	// Test when the environment variable is set
	os.Setenv("WORKSPACES_API_URL", "http://example.com")
	if !Enabled() {
		t.Error("Expected Enabled() to return true when WORKSPACES_API_URL is set")
	}
}
