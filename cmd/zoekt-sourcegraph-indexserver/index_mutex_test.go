package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndexMutexRepositoryIdentity(t *testing.T) {
	var mu indexMutex
	require.True(t, mu.With("repo", 1, func() {
		require.False(t, mu.With("renamed", 1, func() {}))
		require.False(t, mu.With("repo", 2, func() {}))
		require.True(t, mu.With("unrelated", 3, func() {}))
	}))
	require.True(t, mu.With("renamed", 1, func() {}))
}
