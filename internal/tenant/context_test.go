package tenant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/zoekt/internal/tenant/tenanttest"
	"github.com/sourcegraph/zoekt/trace"
)

func TestLog(t *testing.T) {
	tenanttest.MockEnforce(t)

	cases := []struct {
		name          string
		ctx           context.Context
		expectedCount int64
	}{
		{
			name:          "With Tenant",
			ctx:           tenanttest.NewTestContext(),
			expectedCount: 0,
		},
		{
			name:          "Skip Logging",
			ctx:           WithSkipMissingLogging(context.Background()),
			expectedCount: 0,
		},
		{
			name:          "Missing Tenant",
			ctx:           context.Background(),
			expectedCount: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, ctx := trace.New(tc.ctx, "test", "test")
			Log(ctx, tr)
			require.Equal(t, tc.expectedCount, pprofUniqID.Load())
		})
	}
}
