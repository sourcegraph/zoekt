package languages

import (
	"testing"

	"github.com/go-enry/go-enry/v2"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

var cppCapitalExtContent = `// Sample C++ file from the ROSE compiler project
// Original source: https://github.com/rose-compiler/rose
// This file is used for testing C++ language detection for files with .C extension
// Attribution: ROSE Compiler Team - Lawrence Livermore National Laboratory

#include "sage3basic.h"
#include "rose_config.h"

#include "SageTreeBuilder.h"
#include "Jovial_to_ROSE_translation.h"
#include "ModuleBuilder.h"

#include <boost/optional/optional_io.hpp>
#include <iostream>

namespace Rose {
namespace builder {

using namespace Rose::Diagnostics;

namespace SB = SageBuilder;
namespace SI = SageInterface;
namespace LT = LanguageTranslation;
`

func TestGetLanguages(t *testing.T) {
	const matlabContent = "function [out] = square(x)\nout = x * x;\nend"
	const mathematicaContent = "f[x_] := x ^ 2\ng[y_] := f[y]"
	const cppContent = "namespace x { }"
	const cContent = "typedef struct { int x; } Int;"
	const emptyContent = ""

	testCases := []struct {
		path              string
		content           string
		expectedLanguages []string
		compareFirstOnly  bool
	}{
		{path: "perlscript", content: "#!/usr/bin/env perl\n$version = $ARGV[0];", expectedLanguages: []string{"Perl"}},
		{path: "rakuscript", content: "#!/usr/bin/env perl6\n$version = $ARGV[0];", expectedLanguages: []string{"Raku"}},
		{path: "ambiguous.h", content: emptyContent, expectedLanguages: []string{"C", "C++", "Objective-C"}},
		{path: "cpp.h", content: cppContent, expectedLanguages: []string{"C++"}},
		{path: "c.h", content: cContent, expectedLanguages: []string{"C"}},
		{path: "matlab.m", content: matlabContent, expectedLanguages: []string{"MATLAB"}, compareFirstOnly: true},
		{path: "mathematica.m", content: mathematicaContent, expectedLanguages: []string{"Mathematica"}, compareFirstOnly: true},
		{
			path: "mathematica2.m",
			content: `
s := StringRiffle[{"a", "b", "c", "d", "e"}, ", "]
Flatten[{{a, b}, {c, {d}, e}, {f, {g, h}}}]
square[x_] := x ^ 2
fourthpower[x_] := square[square[x]]
`,
			expectedLanguages: []string{"Mathematica"},
			compareFirstOnly:  true,
		},
		{path: "SageTreeBuilder.C", content: cppCapitalExtContent, expectedLanguages: []string{"C++"}},
		// Ported cases from internal/languages TestGetLanguage
		{path: "", content: emptyContent, expectedLanguages: nil},
		{path: "file.unknown", content: emptyContent, expectedLanguages: nil},
		{path: "file.go", content: "package main", expectedLanguages: []string{"Go"}},
		{path: "file.magik", content: emptyContent, expectedLanguages: []string{"Magik"}},
		{path: "file.apxc", content: emptyContent, expectedLanguages: []string{"Apex"}},
		// Check that we classify cls files by content and not just by extension
		{path: "tex.cls", content: `\DeclareOption*{}`, expectedLanguages: []string{"TeX", "Apex", "ObjectScript", "Visual Basic 6.0", "OpenEdge ABL", "VBA"}},
		{path: "tex.cls", content: `public class HelloWorld {`, expectedLanguages: []string{"Apex", "Visual Basic 6.0", "TeX", "OpenEdge ABL", "ObjectScript", "VBA"}},
	}

	for _, testCase := range testCases {
		var getContent func() ([]byte, error)
		if testCase.content != "" {
			getContent = func() ([]byte, error) { return []byte(testCase.content), nil }
		}
		gotLanguages, err := GetLanguages(testCase.path, getContent)
		require.NoError(t, err)
		if testCase.compareFirstOnly {
			require.Equal(t, testCase.expectedLanguages, gotLanguages[0:1])
			continue
		}
		require.Equal(t, testCase.expectedLanguages, gotLanguages)
	}

	rapid.Check(t, func(t *rapid.T) {
		path := rapid.String().Draw(t, "path")
		content := rapid.SliceOfN(rapid.Byte(), 0, 100).Draw(t, "contents")
		require.NotPanics(t, func() {
			langs, err := GetLanguages(path, func() ([]byte, error) { return content, nil })
			require.NoError(t, err)
			if len(langs) != 0 {
				for _, l := range langs {
					require.NotEqual(t, enry.OtherLanguage, l)
				}
			}
		})
	})

	rapid.Check(t, func(t *rapid.T) {
		baseName := "abcd"
		exts := []string{".h", ".m", ".unknown", ""}
		extGens := []*rapid.Generator[string]{}
		for _, ext := range exts {
			extGens = append(extGens, rapid.Just(ext))
		}
		extension := rapid.OneOf(extGens...).Draw(t, "extension")
		path := baseName + extension
		contentGens := []*rapid.Generator[string]{}
		for _, content := range []string{cContent, cppContent, mathematicaContent, matlabContent, emptyContent} {
			contentGens = append(contentGens, rapid.Just(content))
		}
		content := rapid.OneOf(contentGens...).Draw(t, "content")
		langs, err := GetLanguages(path, func() ([]byte, error) {
			return []byte(content), nil
		})
		require.NoError(t, err)
		for _, lang := range langs {
			require.NotEqual(t, enry.OtherLanguage, lang)
		}
	})
}

func TestGetLanguageByNameOrAlias(t *testing.T) {
	tests := []struct {
		name   string
		alias  string
		want   string
		wantOk bool
	}{
		{
			name:   "empty alias",
			alias:  "",
			want:   "",
			wantOk: false,
		},
		{
			name:   "unknown alias",
			alias:  "unknown",
			want:   "",
			wantOk: false,
		},
		{
			name:   "supported alias",
			alias:  "go",
			want:   "Go",
			wantOk: true,
		},
		{
			name:   "unsupported by linguist alias",
			alias:  "magik",
			want:   "Magik",
			wantOk: true,
		},
		{
			name:   "unsupported by linguist alias normalized",
			alias:  "mAgIk",
			want:   "Magik",
			wantOk: true,
		},
		{
			name:   "apex example unsupported by linguist alias",
			alias:  "apex",
			want:   "Apex",
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GetLanguageByNameOrAlias(tt.alias)
			if got != tt.want || ok != tt.wantOk {
				t.Errorf("GetLanguageByNameOrAlias(%q) = %q, %t, want %q, %t", tt.alias, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}
