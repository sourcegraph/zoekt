package zoekt

import (
	"context"

	"github.com/sourcegraph/zoekt/internal/tenant"
	"github.com/sourcegraph/zoekt/query"
)

type tenantAwareSearcher struct {
	d *indexData
}

func (t *tenantAwareSearcher) Search(ctx context.Context, q query.Q, opts *SearchOptions) (*SearchResult, error) {
	ok, err := tenant.IsTenantPath(ctx, t.d.IndexFilePath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return &SearchResult{}, nil
	}
	return t.d.Search(ctx, q, opts)
}

func (t *tenantAwareSearcher) List(ctx context.Context, q query.Q, opts *ListOptions) (*RepoList, error) {
	ok, err := tenant.IsTenantPath(ctx, t.d.IndexFilePath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return &RepoList{}, nil
	}
	return t.d.List(ctx, q, opts)
}

func (t *tenantAwareSearcher) Close() {
	t.d.Close()
}

func (t *tenantAwareSearcher) String() string {
	return t.d.String()
}
