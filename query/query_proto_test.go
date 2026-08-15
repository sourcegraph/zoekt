package query

import (
	"regexp/syntax"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/grafana/regexp"

	webserverv1 "github.com/sourcegraph/zoekt/grpc/protos/zoekt/webserver/v1"
)

func TestQueryRoundtrip(t *testing.T) {
	testCases := []Q{
		&Regexp{
			Regexp:        regexpMustParse("foo"),
			FileName:      true,
			Content:       true,
			CaseSensitive: true,
		},
		&Symbol{
			Expr: &Language{
				Language: "go",
			},
		},
		&Language{
			Language: "typescript",
		},
		&Const{
			Value: true,
		},
		&Repo{
			Regexp: regexp.MustCompile("github.com/foo/bar"),
		},
		&RepoRegexp{
			Regexp: regexp.MustCompile("github.com/foo.*"),
		},
		&BranchesRepos{
			List: []BranchRepos{{
				Branch: "test",
				Repos: func() *roaring.Bitmap {
					bm := roaring.New()
					bm.Add(3)
					bm.Add(34)
					return bm
				}(),
			}},
		},
		NewRepoIDs(3, 4, 5),
		&Branch{
			Pattern: "master",
			Exact:   true,
		},
		NewRepoSet("test1", "test2"),
		NewFileNameSet("test3", "test4"),
		&And{
			Children: []Q{
				&Language{Language: "go"},
				&Type{
					Child: &Substring{Pattern: "interface"},
					Type:  TypeFileMatch,
				},
			},
		},
		&Or{
			Children: []Q{
				&Language{Language: "go"},
				&Type{
					Child: &Substring{Pattern: "interface"},
					Type:  TypeFileMatch,
				},
			},
		},
		&Not{
			Child: &Language{Language: "go"},
		},
		&Boost{
			Child: &Or{
				Children: []Q{
					&And{
						Children: []Q{
							&Substring{Pattern: "foo"},
							&Substring{Pattern: "bar"},
						},
					},
					&Substring{Pattern: "foo bar"},
				},
			},
			Boost: 20,
		},
	}

	for _, q := range testCases {
		t.Run("", func(t *testing.T) {
			protoQ := QToProto(q)
			q2, err := QFromProto(protoQ)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(q.String(), q2.String()); diff != "" {
				t.Fatalf("unexpected diff: %s", diff)
			}
		})
	}
}

func TestRegexpProtoUsesRegexpString(t *testing.T) {
	q := &Regexp{Regexp: regexpMustParse(`a.*b`)}
	protoQ := q.ToProto()

	if got, want := protoQ.GetRegexp(), q.RegexpString(); got != want {
		t.Fatalf("ToProto().Regexp = %q, want %q", got, want)
	}
}

// A query node arrives straight off the wire, so an absent node or an unset
// oneof has to come back as an error. Both used to panic, which took down the
// whole server since there is no recovery interceptor upstream of the handler.
func TestQFromProtoRejectsMissingNodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    *webserverv1.Q
	}{
		{
			name: "nil node, as returned by GetQuery on a request with no query",
			q:    (&webserverv1.SearchRequest{}).GetQuery(),
		},
		{
			name: "node present but oneof unset",
			q:    &webserverv1.Q{},
		},
		{
			name: "child node with oneof unset",
			q: &webserverv1.Q{Query: &webserverv1.Q_And{And: &webserverv1.And{
				Children: []*webserverv1.Q{{}},
			}}},
		},
		{
			name: "nil child node",
			q: &webserverv1.Q{Query: &webserverv1.Q_Or{Or: &webserverv1.Or{
				Children: []*webserverv1.Q{nil},
			}}},
		},
		{
			name: "wrapper with no child",
			q:    &webserverv1.Q{Query: &webserverv1.Q_Not{Not: &webserverv1.Not{}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := QFromProto(tc.q)
			if err == nil {
				t.Fatalf("QFromProto returned %v, want an error", q)
			}
		})
	}
}

func regexpMustParse(s string) *syntax.Regexp {
	re, err := syntax.Parse(s, syntax.Perl)
	if err != nil {
		panic(err)
	}
	return re
}
