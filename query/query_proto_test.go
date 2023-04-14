package query

import (
	"regexp/syntax"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/google/go-cmp/cmp"
	"github.com/grafana/regexp"
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

func regexpMustParse(s string) *syntax.Regexp {
	re, err := syntax.Parse(s, syntax.Perl)
	if err != nil {
		panic(err)
	}
	return re
}
