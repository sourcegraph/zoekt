package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.txt")

	alice := ownerChecker{
		Path:     path,
		Hostname: "alice",
	}
	bob := ownerChecker{
		Path:     path,
		Hostname: "bob",
	}

	assertSuccess := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	assertFailed := func(err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected failure")
		}
	}

	assertSuccess(alice.Init())  // empty dir so success
	assertSuccess(alice.Check()) // alice took ownership above
	assertSuccess(bob.Init())    // bob is now the owner. Only debug logs about change of ownership.
	assertFailed(alice.Check())  // alice is not the owner anymore
	assertSuccess(bob.Check())   // bob is still the owner

	// Test what happens if someone corrupts the file
	if err := os.WriteFile(path, []byte("!corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertFailed(alice.Check()) // corrupt so fail
	assertFailed(bob.Check())   // corrupt so fail
	assertSuccess(bob.Init())   // bob ovewrites corruption
	assertSuccess(bob.Check())  // bob is the owner
	assertFailed(alice.Check()) // alice is not the owner
}
