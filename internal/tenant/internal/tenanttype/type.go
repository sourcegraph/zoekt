package tenanttype

import (
	"context"
	"fmt"
	"runtime/pprof"
	"strconv"

	"github.com/sourcegraph/zoekt/internal/tenant/internal/enforcement"

	"go.uber.org/atomic"
)

type Tenant struct {
	// never expose this otherwise impersonation outside of this package is possible.
	_id int
}

func (t *Tenant) ID() int {
	return t._id
}

type contextKey int

const tenantKey contextKey = iota

// WithTenant returns a new context for the given tenant.
func WithTenant(ctx context.Context, tenant *Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, tenant)
}

const skipLogging contextKey = iota

var ErrNoTenantInContext = fmt.Errorf("no tenant in context")

func FromContext(ctx context.Context) (*Tenant, error) {
	tnt, ok := ctx.Value(tenantKey).(*Tenant)
	if !ok {
		if pprofMissingTenant != nil {
			_, ok := ctx.Value(skipLogging).(contextKey)
			if !ok {
				// We want to track every stack trace, so need a unique value for the event
				eventValue := pprofUniqID.Add(1)

				// skip stack for Add and this function (2).
				pprofMissingTenant.Add(eventValue, 2)
			}
		}

		return nil, ErrNoTenantInContext
	}
	return tnt, nil
}

var pprofUniqID atomic.Int64
var pprofMissingTenant = func() *pprof.Profile {
	if !enforcement.ShouldLogNoTenant() {
		return nil
	}
	return pprof.NewProfile("missing_tenant")
}()

func Unmarshal(s string) (*Tenant, error) {
	id, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("bad tenant value: %q: %w", s, err)
	}
	return FromID(id)
}

func Marshal(t *Tenant) string {
	return strconv.Itoa(t._id)
}

func FromID(id int) (*Tenant, error) {
	if id < 1 {
		return nil, fmt.Errorf("invalid tenant id: %d", id)
	}
	return &Tenant{_id: id}, nil
}
