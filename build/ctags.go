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

package build

import (
	"bytes"
	"fmt"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/ctags"
)

func ctagsAddSymbolsParser(todo []*zoekt.Document, parser ctags.Parser) error {
	for _, doc := range todo {
		if doc.Symbols != nil {
			continue
		}

		es, err := parser.Parse(doc.Name, doc.Content)
		if err != nil {
			return err
		}
		if len(es) == 0 {
			continue
		}

		symOffsets, symMetaData, err := tagsToSections(doc.Content, es)
		if err != nil {
			return fmt.Errorf("%s: %v", doc.Name, err)
		}
		doc.Symbols = symOffsets
		doc.SymbolsMetaData = symMetaData
	}

	return nil
}

// overlaps finds the proper position to insert a zoekt.DocumentSection with
// "start and "end" into "symOffsets". It returns -1 if the new section overlaps
// with one of the existing ones.
func overlaps(symOffsets []zoekt.DocumentSection, start, end uint32) int {
	i := 0
	for i = len(symOffsets) - 1; i >= 0; i-- {
		// The most common case is that we exit here, because symOffsets is sorted by
		// construction and start is in many cases monotonically increasing.
		if start >= symOffsets[i].End {
			break
		}
		if end <= symOffsets[i].Start {
			continue
		}
		// overlap
		return -1
	}
	return i + 1
}

// tagsToSections converts ctags entries to byte ranges (zoekt.DocumentSection)
// with corresponding metadata (zoekt.Symbol).
func tagsToSections(content []byte, tags []*ctags.Entry) ([]zoekt.DocumentSection, []*zoekt.Symbol, error) {
	nls := newLinesIndices(content)
	nls = append(nls, uint32(len(content)))
	var symOffsets []zoekt.DocumentSection
	var symMetaData []*zoekt.Symbol

	for _, t := range tags {
		if t.Line <= 0 {
			// Observed this with a .JS file.
			continue
		}
		lineIdx := t.Line - 1
		if lineIdx >= len(nls) {
			return nil, nil, fmt.Errorf("linenum for entry out of range %v", t)
		}

		lineOff := uint32(0)
		if lineIdx > 0 {
			lineOff = nls[lineIdx-1] + 1
		}

		end := nls[lineIdx]
		line := content[lineOff:end]

		// This is best-effort only. For short symbol names, we will often determine the
		// wrong offset.
		intraOff := bytes.Index(line, []byte(t.Name))
		if intraOff < 0 {
			// for Go code, this is very common, since
			// ctags barfs on multi-line declarations
			continue
		}

		start := lineOff + uint32(intraOff)
		endSym := start + uint32(len(t.Name))

		i := overlaps(symOffsets, start, endSym)
		if i == -1 {
			// Detected an overlap. Give up.
			continue
		}

		symOffsets = append(
			symOffsets[:i],
			append([]zoekt.DocumentSection{{Start: start, End: endSym}}, symOffsets[i:]...)...,
		)
		symMetaData = append(
			symMetaData[:i],
			append(
				[]*zoekt.Symbol{{Sym: t.Name, Kind: t.Kind, Parent: t.Parent, ParentKind: t.ParentKind}},
				symMetaData[i:]...,
			)...,
		)
	}

	return symOffsets, symMetaData, nil
}

func newLinesIndices(in []byte) []uint32 {
	out := make([]uint32, 0, len(in)/30)
	for i, c := range in {
		if c == '\n' {
			out = append(out, uint32(i))
		}
	}
	return out
}
