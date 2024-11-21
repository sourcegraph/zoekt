package systemtenant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemtenantRoundtrip(t *testing.T) {
	if Is(context.Background()) {
		t.Fatal()
	}
	ctx, err := With(context.Background())
	require.NoError(t, err)
	require.True(t, Is(ctx))
}
