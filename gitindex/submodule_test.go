// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gitindex

import (
	"reflect"
	"testing"
)

func TestParseGitModules(t *testing.T) {
	cases := []struct {
		data string
		want map[string]*SubmoduleEntry
	}{{
		`[submodule "plugins/abc"]
		path = plugins/abc
		url = ../plugins/abc
		branch = .`,
		map[string]*SubmoduleEntry{
			"plugins/abc": {
				Path:   "plugins/abc",
				URL:    "../plugins/abc",
				Branch: ".",
			},
		}},
		{
			"\uFEFF" + `[submodule "plugins/abc"]
			path = plugins/abc
			url = ../plugins/abc
			branch = .`,
			map[string]*SubmoduleEntry{
				"plugins/abc": {
					Path:   "plugins/abc",
					URL:    "../plugins/abc",
					Branch: ".",
				},
			}},
		{"", map[string]*SubmoduleEntry{}},
	}

	for _, tc := range cases {
		got, err := ParseGitModules([]byte(tc.data))
		if err != nil {
			t.Fatalf("ParseGitModules: %T", err)
		}

		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("got %v, want %v", got, tc.want)
		}
	}
}
