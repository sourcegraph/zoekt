// Copyright 2017 Google Inc. All rights reserved.
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

package ctags

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/sourcegraph/zoekt/internal/testutil"
)

func TestJSON(t *testing.T) {
	bin := testutil.RequireCTags(t)

	p := NewCTagsParser(map[CTagsParserType]string{UniversalCTags: bin})
	defer p.Close()

	java := `
package io.zoekt;
import java.util.concurrent.Future;
class Back implements Future extends Frob {
  public static int BLA = 1;
  public int member;
  public Back() {
    member = 2;
  }
  public int method() {
    member++;
  }
}
`
	name := "io/zoekt/Back.java"
	got, err := p.Parse(name, []byte(java), UniversalCTags)
	if err != nil {
		t.Errorf("Process: %v", err)
	}

	want := []*Entry{
		{
			Name:     "io.zoekt",
			Kind:     "package",
			Language: "Java",
			Path:     "io/zoekt/Back.java",
			Line:     2,
		},
		{
			Name:     "Back",
			Path:     "io/zoekt/Back.java",
			Line:     4,
			Kind:     "classes",
			Language: "Java",
			Pattern:  "/^class Back implements Future extends Frob {$/",
		},
		{
			Name:     "Back",
			Path:     "io/zoekt/Back.java",
			Line:     4,
			Language: "Java",
			Kind:     "class",
		},
		{
			Name:       "BLA",
			Path:       "io/zoekt/Back.java",
			Line:       5,
			Kind:       "field",
			Language:   "Java",
			Parent:     "Back",
			ParentKind: "class",
		},
		{
			Name:       "member",
			Path:       "io/zoekt/Back.java",
			Line:       6,
			Language:   "Java",
			Kind:       "field",
			Parent:     "Back",
			ParentKind: "class",
		},
		{
			Name:       "Back",
			Path:       "io/zoekt/Back.java",
			Language:   "Java",
			Line:       7,
			Kind:       "method",
			Parent:     "Back",
			ParentKind: "class",
		},
		{
			Name:       "method",
			Language:   "Java",
			Path:       "io/zoekt/Back.java",
			Line:       10,
			Kind:       "method",
			Parent:     "Back",
			ParentKind: "class",
		},
	}

	if d := cmp.Diff(want, got, cmpopts.IgnoreFields(Entry{}, "Pattern", "Signature")); d != "" {
		t.Errorf("mismatch (-want +got):\n%s", d)
	}
}
