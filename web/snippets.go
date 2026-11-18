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

package web

import (
	"bytes"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"text/template"

	"github.com/sourcegraph/zoekt"
)

func (s *Server) formatResults(result *zoekt.SearchResult, query string, localPrint bool) ([]*FileMatch, error) {
	var fmatches []*FileMatch

	templateMap := map[string]*template.Template{}
	fragmentMap := map[string]*template.Template{}
	if !localPrint {
		for repo, str := range result.RepoURLs {
			if str != "" {
				templateMap[repo] = s.getTextTemplate(str)
			}
		}
		for repo, str := range result.LineFragments {
			if str != "" {
				fragmentMap[repo] = s.getTextTemplate(str)
			}
		}
	}
	getFragment := func(repo string, linenum int) string {
		tpl := fragmentMap[repo]

		if tpl == nil || localPrint {
			return "#l" + strconv.Itoa(linenum)
		}

		var buf bytes.Buffer
		if err := tpl.Execute(&buf, map[string]string{
			"LineNumber": strconv.Itoa(linenum),
		}); err != nil {
			slog.Warn("failed to execute fragment template", "error", err)
			return ""
		}
		return buf.String()
	}
	// getURL returns the link for f and whether it goes to the print page.
	getURL := func(f *zoekt.FileMatch) (string, bool) {
		// A subrepository's URL template takes paths relative to its root.
		repo, filename := f.Repository, f.FileName
		if f.SubRepositoryName != "" {
			repo = f.SubRepositoryName
			filename = strings.TrimPrefix(f.FileName[len(f.SubRepositoryPath):], "/")
		}

		tpl := templateMap[repo]
		if localPrint || tpl == nil {
			// The print page finds files by their indexed repository and full
			// path, which for subrepository files are the superproject's.
			v := make(url.Values)
			v.Add("r", f.Repository)
			v.Add("f", f.FileName)
			v.Add("q", query)
			if len(f.Branches) > 0 {
				v.Add("b", f.Branches[0])
			}
			return "print?" + v.Encode(), true
		}

		var buf bytes.Buffer
		b := ""
		if len(f.Branches) > 0 {
			b = f.Branches[0]
		}
		err := tpl.Execute(&buf, map[string]string{
			"Branch":  b,
			"Version": f.Version,
			"Path":    filename,
		})
		if err != nil {
			slog.Warn("failed to execute url template", "error", err)
			return "", false
		}
		return buf.String(), false
	}

	// hash => result-id
	seenFiles := map[string]string{}
	for _, f := range result.Files {
		fMatch := FileMatch{
			FileName:   f.FileName,
			Repo:       f.Repository,
			ResultID:   f.Repository + ":" + f.FileName,
			Branches:   f.Branches,
			Language:   f.Language,
			Score:      f.Score,
			ScoreDebug: f.Debug,
		}

		if dup, ok := seenFiles[string(f.Checksum)]; ok {
			fMatch.DuplicateID = dup
		} else {
			seenFiles[string(f.Checksum)] = fMatch.ResultID
		}

		var printPage bool
		fMatch.URL, printPage = getURL(&f)

		for _, m := range f.LineMatches {
			// The print page names its lines #lN, whatever the line fragment
			// template says.
			fragment := "#l" + strconv.Itoa(m.LineNumber)
			if !printPage {
				fragment = getFragment(f.Repository, m.LineNumber)
			}
			if !strings.HasPrefix(fragment, "#") && !strings.HasPrefix(fragment, ";") {
				// TODO - remove this is backward compatibility glue.
				fragment = "#" + fragment
			}
			md := Match{
				FileName: f.FileName,
				LineNum:  m.LineNumber,
				URL:      fMatch.URL + fragment,

				Score:      m.Score,
				ScoreDebug: m.DebugScore,
			}

			md.Before = string(m.Before)
			md.After = string(m.After)
			lastEnd := 0
			line := m.Line
			for i, f := range m.LineFragments {
				l := f.LineOffset
				e := l + f.MatchLength

				frag := Fragment{
					Pre:   string(line[lastEnd:l]),
					Match: string(line[l:e]),
				}
				if i == len(m.LineFragments)-1 {
					frag.Post = string(m.Line[e:])
				}

				md.Fragments = append(md.Fragments, frag)
				lastEnd = e
			}
			fMatch.Matches = append(fMatch.Matches, md)
		}
		fmatches = append(fmatches, &fMatch)
	}
	return fmatches, nil
}
