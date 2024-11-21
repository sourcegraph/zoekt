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
	require.True(t, Is(Ctx))
}
