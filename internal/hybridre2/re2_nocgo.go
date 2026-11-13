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

//go:build !re2_cgo

package hybridre2

import "errors"

// re2Available is false: RE2 is only linked in builds with -tags re2_cgo.
const re2Available = false

type re2Engine struct{}

func (*re2Engine) FindAllIndex([]byte, int) [][]int { return nil }

func compileRE2(string) (*re2Engine, error) {
	return nil, errors.New("hybridre2: built without -tags re2_cgo")
}
