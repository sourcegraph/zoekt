package query

import (
	"regexp/syntax"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
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
			Expr: &Substring{Pattern: "symbol"},
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
		&Meta{
			Field: "license",
			Value: regexp.MustCompile("^MIT$"),
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

// Bounded repeats must survive serialization. Expanding them into nested
// optional groups produces patterns Go's regexp parser rejects as too deeply
// nested once the upper bound is in the hundreds.
func TestQueryRoundtripBoundedRepeat(t *testing.T) {
	for _, s := range []string{`foo.{0,500}bar`, `foo.{1,1000}bar`, `(ab){0,1000}`} {
		t.Run(s, func(t *testing.T) {
			q, err := Parse(s)
			if err != nil {
				t.Fatal(err)
			}
			q2, err := QFromProto(QToProto(q))
			if err != nil {
				t.Fatalf("%.100s", err)
			}
			if diff := cmp.Diff(q.String(), q2.String()); diff != "" {
				t.Fatalf("unexpected diff: %s", diff)
			}
		})
	}
}

func TestSymbolFromProtoRejectsNonTextAtom(t *testing.T) {
	p := (&Symbol{Expr: &Language{Language: "go"}}).ToProto()
	if _, err := SymbolFromProto(p); err == nil {
		t.Fatal("SymbolFromProto succeeded for a non-text expression")
	}
}

func TestRegexpProtoUsesRegexpString(t *testing.T) {
	q := &Regexp{Regexp: regexpMustParse(`a.*b`)}
	protoQ := q.ToProto()

	if got, want := protoQ.GetRegexp(), q.RegexpString(); got != want {
		t.Fatalf("ToProto().Regexp = %q, want %q", got, want)
	}
}

func regexpMustParse(s string) *syntax.Regexp {
	re, err := syntax.Parse(s, syntax.Perl)
	if err != nil {
		panic(err)
	}
	return re
}
