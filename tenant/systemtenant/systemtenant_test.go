package systemtenant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemTenantRoundTrip(t *testing.T) {
	if Is(context.Background()) {
		t.Fatal()
	}
	require.True(t, Is(WithUnsafeContext(context.Background())))
}
