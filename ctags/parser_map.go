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
	"fmt"
)

type CTagsParserType uint8

const (
	UnknownCTags CTagsParserType = iota
	NoCTags
	UniversalCTags
	ScipCTags
)

type LanguageMap = map[string]CTagsParserType

func ParserToString(parser CTagsParserType) string {
	switch parser {
	case UnknownCTags:
		return "unknown"
	case NoCTags:
		return "no"
	case UniversalCTags:
		return "universal"
	case ScipCTags:
		return "scip"
	default:
		panic("Reached impossible CTagsParserType state")
	}
}

func StringToParser(str string) CTagsParserType {
	switch str {
	case "no":
		return NoCTags
	case "universal":
		return UniversalCTags
	case "scip":
		return ScipCTags
	default:
		return UniversalCTags
	}
}

type ParserMap map[CTagsParserType]Parser
type ParserBinMap map[CTagsParserType]string

func NewParserMap(bins ParserBinMap, languageMap LanguageMap, cTagsMustSucceed bool) (ParserMap, error) {
	parsers := make(ParserMap)

	requiredTypes := []CTagsParserType{UniversalCTags}
	for _, parserType := range languageMap {
		if parserType == ScipCTags {
			requiredTypes = append(requiredTypes, ScipCTags)
			break
		}
	}

	for _, parserType := range requiredTypes {
		bin := bins[parserType]
		if bin == "" && cTagsMustSucceed {
			return nil, fmt.Errorf("ctags binary not found for %s parser type", ParserToString(parserType))
		} else {
			parser, err := NewParser(parserType, bin)
			if err != nil && cTagsMustSucceed {
				return nil, fmt.Errorf("ctags.NewParserMap: %v", err)
			}

			parsers[parserType] = parser
		}
	}

	return parsers, nil
}
