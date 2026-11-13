// Copyright 2026 Google Inc. All rights reserved.
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

//go:build re2_cgo

package hybridre2

import re2regexp "github.com/wasilibs/go-re2"

// re2Available reports whether this binary links RE2 (built with -tags re2_cgo).
const re2Available = true

type re2Engine = re2regexp.Regexp

func compileRE2(pattern string) (*re2Engine, error) {
	return re2regexp.Compile(pattern)
}
