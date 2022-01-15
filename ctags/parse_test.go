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

package ctags

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	type testcase struct {
		in  string
		out *Entry
	}
	cases := []testcase{
		{
			`ABBREV_SHA	./gitiles-servlet/src/main/java/com/google/gitiles/CommitData.java	59;"	e	enum:CommitData.Field	file:`,
			&Entry{
				Name:        "ABBREV_SHA",
				Path:        "./gitiles-servlet/src/main/java/com/google/gitiles/CommitData.java",
				Line:        59,
				Kind:        "e",
				Parent:      "CommitData.Field",
				ParentKind:  "enum",
				FileLimited: true,
			},
		},
		{
			`ACCESS_ATTRIBUTE	./gitiles-servlet/src/main/java/com/google/gitiles/CommitData.java	55;"	f	class:BaseServlet	file:`,
			&Entry{
				Name:        "ACCESS_ATTRIBUTE",
				Path:        "./gitiles-servlet/src/main/java/com/google/gitiles/CommitData.java",
				Line:        55,
				Kind:        "f",
				Parent:      "BaseServlet",
				ParentKind:  "class",
				FileLimited: true,
			},
		},
	}
	for _, c := range cases {
		e, err := Parse(c.in)
		if err != nil && c.out != nil {
			t.Errorf("Parse(%s): %v", c.in, err)
		} else if !reflect.DeepEqual(c.out, e) {
			t.Errorf("Parse(%s): got %#v, want %#v", c.in, e, c.out)
		}
	}
}
