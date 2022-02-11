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

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/grafana/regexp"
)

// I will go to programmer hell for trying to parse HTML with
// regexps. Why doesn't CGit have a JSON interface?
var cgitRepoEntryRE = regexp.MustCompile(
	`class='sublevel-repo'><a title='([^'"]*)' href='([^']*)'>`)

func normalizedGet(u *url.URL) ([]byte, error) {
	rep, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer rep.Body.Close()
	if rep.StatusCode != 200 {
		return nil, fmt.Errorf("status %s", rep.Status)
	}

	c, err := ioutil.ReadAll(rep.Body)
	if err != nil {
		return nil, err
	}

	c = bytes.Replace(c, []byte{'\n'}, []byte{' '}, -1)
	return c, nil
}

// getCGitRepos finds repo names from the CGit index page hosted at
// URL `u`.
func getCGitRepos(u *url.URL, filter func(string) bool) (map[string]*crawlTarget, error) {
	c, err := normalizedGet(u)
	if err != nil {
		return nil, err
	}

	pages := map[string]*crawlTarget{}
	for _, m := range cgitRepoEntryRE.FindAllSubmatch(c, -1) {
		nm := strings.TrimSuffix(string(m[1]), ".git")

		if !filter(nm) {
			continue
		}

		relUrl := string(m[2])

		u, err := u.Parse(relUrl)
		if err != nil {
			log.Printf("ignoring u.Parse(%q): %v", relUrl, err)
			continue
		}
		pages[nm] = &crawlTarget{
			webURL:     u.String(),
			webURLType: "cgit",
		}
	}

	// TODO - parallel?
	for _, target := range pages {
		u, _ := url.Parse(target.webURL)
		c, err := cgitCloneURL(u)
		if err != nil {
			log.Printf("ignoring cgitCloneURL(%s): %v", u, c)
			continue
		}

		target.cloneURL = c.String()
	}
	return pages, nil
}

// We'll take the first URL we get. This may put the git:// URL (which
// is insecure) at the top, but individual machines (such as
// git.savannah.gnu) probably would rather receive git:// traffic
// which is more efficient.

// TODO - do something like `Clone.*<a.*href=` to get the first
// URL. Older versions don't say vcs-git.
var cloneURLRe = regexp.MustCompile(
	`rel=["']vcs-git["'] *href=["']([^"']*)["']`)

func cgitCloneURL(u *url.URL) (*url.URL, error) {
	c, err := normalizedGet(u)
	if err != nil {
		return nil, err
	}

	m := cloneURLRe.FindSubmatch(c)
	cl, err := url.Parse(string(m[1]))
	if err != nil {
		return nil, err
	}

	return cl, nil
}
