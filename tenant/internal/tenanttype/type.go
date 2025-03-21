package tenanttype

import (
	"context"
	"fmt"
	"strconv"
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

func GetTenant(ctx context.Context) (*Tenant, bool) {
	tnt, ok := ctx.Value(tenantKey).(*Tenant)
	return tnt, ok
}

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
