package mockSearcher

import (
	"context"
	"fmt"

	"github.com/xvandish/zoekt"
	"github.com/xvandish/zoekt/query"
)

type MockSearcher struct {
	WantSearch   query.Q
	SearchResult *zoekt.SearchResult

	WantList query.Q
	RepoList *zoekt.RepoList
}

func (s *MockSearcher) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	if q.String() != s.WantSearch.String() {
		return nil, fmt.Errorf("got query %s != %s", q.String(), s.WantSearch.String())
	}
	return s.SearchResult, nil
}

func (s *MockSearcher) List(ctx context.Context, q query.Q, opts *zoekt.ListOptions) (*zoekt.RepoList, error) {
	if q.String() != s.WantList.String() {
		return nil, fmt.Errorf("got query %s != %s", q.String(), s.WantList.String())
	}
	return s.RepoList, nil
}

func (*MockSearcher) Close() {}

func (*MockSearcher) String() string {
	return "MockSearcher"
}
