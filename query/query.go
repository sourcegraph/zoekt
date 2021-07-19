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

package query

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"regexp/syntax"
	"sort"
	"strings"
)

var _ = log.Println

// Q is a representation for a possibly hierarchical search query.
type Q interface {
	String() string
}

// RawConfig filters repositories based on their encoded RawConfig map.
type RawConfig uint64

const (
	RcOnlyPublic   RawConfig = 1
	RcOnlyPrivate  RawConfig = 2
	RcOnlyForks    RawConfig = 1 << 2
	RcNoForks      RawConfig = 2 << 2
	RcOnlyArchived RawConfig = 1 << 4
	RcNoArchived   RawConfig = 2 << 4
)

var flagNames = map[RawConfig]string{
	RcOnlyPublic:   "RcOnlyPublic",
	RcOnlyPrivate:  "RcOnlyPrivate",
	RcOnlyForks:    "RcOnlyForks",
	RcNoForks:      "RcNoForks",
	RcOnlyArchived: "RcOnlyArchived",
	RcNoArchived:   "RcNoArchived",
}

func (r RawConfig) String() string {
	var s []string
	for f, label := range flagNames {
		if r&f != 0 {
			s = append(s, label)
		}
	}
	return fmt.Sprintf("rawConfig:%s", strings.Join(s, "|"))
}

// RegexpQuery is a query looking for regular expressions matches.
type Regexp struct {
	Regexp        *syntax.Regexp
	FileName      bool
	Content       bool
	CaseSensitive bool
}

// Symbol finds a string that is a symbol.
type Symbol struct {
	Expr Q
}

func (s *Symbol) String() string {
	return fmt.Sprintf("sym:%s", s.Expr)
}

func (q *Regexp) String() string {
	pref := ""
	if q.FileName {
		pref = "file_"
	}
	if q.CaseSensitive {
		pref = "case_" + pref
	}
	return fmt.Sprintf("%sregex:%q", pref, q.Regexp.String())
}

// gobRegexp wraps Regexp to make it gob-encodable/decodable. Regexp contains syntax.Regexp, which
// contains slices/arrays with possibly nil elements, which gob doesn't support
// (https://github.com/golang/go/issues/1501).
type gobRegexp struct {
	Regexp       // Regexp.Regexp (*syntax.Regexp) is set to nil and its string is set in RegexpString
	RegexpString string
}

// GobEncode implements gob.Encoder.
func (q Regexp) GobEncode() ([]byte, error) {
	gobq := gobRegexp{Regexp: q, RegexpString: q.Regexp.String()}
	gobq.Regexp.Regexp = nil // can't be gob-encoded/decoded
	return json.Marshal(gobq)
}

// GobDecode implements gob.Decoder.
func (q *Regexp) GobDecode(data []byte) error {
	var gobq gobRegexp
	err := json.Unmarshal(data, &gobq)
	if err != nil {
		return err
	}
	gobq.Regexp.Regexp, err = syntax.Parse(gobq.RegexpString, regexpFlags)
	if err != nil {
		return err
	}
	*q = gobq.Regexp
	return nil
}

type caseQ struct {
	Flavor string
}

func (c *caseQ) String() string {
	return "case:" + c.Flavor
}

type Language struct {
	Language string
}

func (l *Language) String() string {
	return "lang:" + l.Language
}

type Const struct {
	Value bool
}

func (q *Const) String() string {
	if q.Value {
		return "TRUE"
	}
	return "FALSE"
}

type Repo struct {
	Pattern string
}

func (q *Repo) String() string {
	return fmt.Sprintf("repo:%s", q.Pattern)
}

// RepoBranches is a list of branches in repos to match. It is a Sourcegraph
// addition and only used in the RPC interface for efficient checking of large
// repo lists.
type RepoBranches struct {
	// Set is map reponame -> [branch]
	Set map[string][]string
}

func (q *RepoBranches) String() string {
	var detail string
	if len(q.Set) > 5 {
		// Large sets being output are not useful
		detail = fmt.Sprintf("size=%d", len(q.Set))
	} else {
		repos := make([]string, len(q.Set))
		i := 0
		for repo, branches := range q.Set {
			// repo@master:develop:master
			repos[i] = fmt.Sprintf("%s@%s", repo, strings.Join(branches, ":"))
			i++
		}
		sort.Strings(repos)
		detail = strings.Join(repos, " ")
	}
	return fmt.Sprintf("(repobranches %s)", detail)
}

// Branches returns a query representing the branches to search for name.
func (q *RepoBranches) Branches(name string) Q {
	branches, ok := q.Set[name]
	if !ok {
		return &Const{Value: false}
	}

	// New sub query is (or (branch branches[0]) ...)
	qs := make([]Q, len(branches))
	for i, branch := range branches {
		qs[i] = &Branch{Pattern: branch, Exact: true}
	}
	return NewOr(qs...)
}

// MarshalBinary implements a specialized encoder for RepoBranches.
func (q *RepoBranches) MarshalBinary() ([]byte, error) {
	return repoBranchesEncode(q.Set)
}

// UnmarshalBinary implements a specialized decoder for RepoBranches.
func (q *RepoBranches) UnmarshalBinary(b []byte) error {
	var err error
	q.Set, err = repoBranchesDecode(b)
	return err
}

// RepoSet is a list of repos to match. It is a Sourcegraph addition and only
// used in the RPC interface for efficient checking of large repo lists.
type RepoSet struct {
	Set map[string]bool
}

func (q *RepoSet) String() string {
	var detail string
	if len(q.Set) > 5 {
		// Large sets being output are not useful
		detail = fmt.Sprintf("size=%d", len(q.Set))
	} else {
		repos := make([]string, len(q.Set))
		i := 0
		for repo := range q.Set {
			repos[i] = repo
			i++
		}
		sort.Strings(repos)
		detail = strings.Join(repos, " ")
	}
	return fmt.Sprintf("(reposet %s)", detail)
}

func NewRepoSet(repo ...string) *RepoSet {
	s := &RepoSet{Set: make(map[string]bool)}
	for _, r := range repo {
		s.Set[r] = true
	}
	return s
}

const (
	TypeFileMatch uint8 = iota
	TypeFileName
	TypeRepo
)

// Type changes the result type returned.
type Type struct {
	Child Q
	Type  uint8
}

func (q *Type) String() string {
	switch q.Type {
	case TypeFileMatch:
		return fmt.Sprintf("(type:filematch %s)", q.Child)
	case TypeFileName:
		return fmt.Sprintf("(type:filename %s)", q.Child)
	case TypeRepo:
		return fmt.Sprintf("(type:repo %s)", q.Child)
	default:
		return fmt.Sprintf("(type:UNKNOWN %s)", q.Child)
	}
}

// Substring is the most basic query: a query for a substring.
type Substring struct {
	Pattern       string
	CaseSensitive bool

	// Match only filename
	FileName bool

	// Match only content
	Content bool
}

func (q *Substring) String() string {
	s := ""

	t := ""
	if q.FileName {
		t = "file_"
	} else if q.Content {
		t = "content_"
	}

	s += fmt.Sprintf("%ssubstr:%q", t, q.Pattern)
	if q.CaseSensitive {
		s = "case_" + s
	}
	return s
}

type setCaser interface {
	setCase(string)
}

func (q *Substring) setCase(k string) {
	switch k {
	case "yes":
		q.CaseSensitive = true
	case "no":
		q.CaseSensitive = false
	case "auto":
		// TODO - unicode
		q.CaseSensitive = (q.Pattern != string(toLower([]byte(q.Pattern))))
	}
}

func (q *Symbol) setCase(k string) {
	if sc, ok := q.Expr.(setCaser); ok {
		sc.setCase(k)
	}
}

func (q *Regexp) setCase(k string) {
	switch k {
	case "yes":
		q.CaseSensitive = true
	case "no":
		q.CaseSensitive = false
	case "auto":
		q.CaseSensitive = (q.Regexp.String() != LowerRegexp(q.Regexp).String())
	}
}

// Or is matched when any of its children is matched.
type Or struct {
	Children []Q
}

func (q *Or) String() string {
	var sub []string
	for _, ch := range q.Children {
		sub = append(sub, ch.String())
	}
	return fmt.Sprintf("(or %s)", strings.Join(sub, " "))
}

// Not inverts the meaning of its child.
type Not struct {
	Child Q
}

func (q *Not) String() string {
	return fmt.Sprintf("(not %s)", q.Child)
}

// And is matched when all its children are.
type And struct {
	Children []Q
}

func (q *And) String() string {
	var sub []string
	for _, ch := range q.Children {
		sub = append(sub, ch.String())
	}
	return fmt.Sprintf("(and %s)", strings.Join(sub, " "))
}

// NewAnd is syntactic sugar for constructing And queries.
func NewAnd(qs ...Q) Q {
	return &And{Children: qs}
}

// NewOr is syntactic sugar for constructing Or queries.
func NewOr(qs ...Q) Q {
	return &Or{Children: qs}
}

// Branch limits search to a specific branch.
type Branch struct {
	Pattern string

	// exact is true if we want to Pattern to equal branch.
	Exact bool
}

func (q *Branch) String() string {
	if q.Exact {
		return fmt.Sprintf("branch=%q", q.Pattern)
	}
	return fmt.Sprintf("branch:%q", q.Pattern)
}

func queryChildren(q Q) []Q {
	switch s := q.(type) {
	case *And:
		return s.Children
	case *Or:
		return s.Children
	}
	return nil
}

func flattenAndOr(children []Q, typ Q) ([]Q, bool) {
	var flat []Q
	changed := false
	for _, ch := range children {
		ch, subChanged := flatten(ch)
		changed = changed || subChanged
		if reflect.TypeOf(ch) == reflect.TypeOf(typ) {
			changed = true
			subChildren := queryChildren(ch)
			if subChildren != nil {
				flat = append(flat, subChildren...)
			}
		} else {
			flat = append(flat, ch)
		}
	}

	return flat, changed
}

// (and (and x y) z) => (and x y z) , the same for "or"
func flatten(q Q) (Q, bool) {
	switch s := q.(type) {
	case *And:
		if len(s.Children) == 1 {
			return s.Children[0], true
		}
		flatChildren, changed := flattenAndOr(s.Children, s)
		return &And{flatChildren}, changed
	case *Or:
		if len(s.Children) == 1 {
			return s.Children[0], true
		}
		flatChildren, changed := flattenAndOr(s.Children, s)
		return &Or{flatChildren}, changed
	case *Not:
		child, changed := flatten(s.Child)
		return &Not{child}, changed
	case *Type:
		child, changed := flatten(s.Child)
		return &Type{Child: child, Type: s.Type}, changed
	default:
		return q, false
	}
}

func mapQueryList(qs []Q, f func(Q) Q) []Q {
	neg := make([]Q, len(qs))
	for i, sub := range qs {
		neg[i] = Map(sub, f)
	}
	return neg
}

func invertConst(q Q) Q {
	c, ok := q.(*Const)
	if ok {
		return &Const{!c.Value}
	}
	return q
}

func evalAndOrConstants(q Q, children []Q) Q {
	_, isAnd := q.(*And)

	children = mapQueryList(children, evalConstants)

	newCH := children[:0]
	for _, ch := range children {
		c, ok := ch.(*Const)
		if ok {
			if c.Value == isAnd {
				continue
			} else {
				return ch
			}
		}
		newCH = append(newCH, ch)
	}
	if len(newCH) == 0 {
		return &Const{isAnd}
	}
	if isAnd {
		return &And{newCH}
	}
	return &Or{newCH}
}

func evalConstants(q Q) Q {
	switch s := q.(type) {
	case *And:
		return evalAndOrConstants(q, s.Children)
	case *Or:
		return evalAndOrConstants(q, s.Children)
	case *Not:
		ch := evalConstants(s.Child)
		if _, ok := ch.(*Const); ok {
			return invertConst(ch)
		}
		return &Not{ch}
	case *Type:
		ch := evalConstants(s.Child)
		if _, ok := ch.(*Const); ok {
			// If q is the root query, then evaluating this to a const changes
			// the type of result we will return. However, the only case this
			// makes sense is `type:repo TRUE` to return all repos or
			// `type:file TRUE` to return all filenames. For other cases we
			// want to do this constant folding though, so we allow the
			// unexpected behaviour mentioned previously.
			return ch
		}
		return &Type{Child: ch, Type: s.Type}
	case *Substring:
		if len(s.Pattern) == 0 {
			return &Const{true}
		}
	case *Regexp:
		if s.Regexp.Op == syntax.OpEmptyMatch {
			return &Const{true}
		}
	case *Branch:
		if s.Pattern == "" {
			return &Const{true}
		}
	case *RepoSet:
		if len(s.Set) == 0 {
			return &Const{true}
		}
	}
	return q
}

func Simplify(q Q) Q {
	q = evalConstants(q)
	for {
		var changed bool
		q, changed = flatten(q)
		if !changed {
			break
		}
	}

	return q
}

// Map runs f over the q.
func Map(q Q, f func(q Q) Q) Q {
	switch s := q.(type) {
	case *And:
		q = &And{Children: mapQueryList(s.Children, f)}
	case *Or:
		q = &Or{Children: mapQueryList(s.Children, f)}
	case *Not:
		q = &Not{Child: Map(s.Child, f)}
	case *Type:
		q = &Type{Type: s.Type, Child: Map(s.Child, f)}
	}
	return f(q)
}

// Expand expands Substr queries into (OR file_substr content_substr)
// queries, and the same for Regexp queries..
func ExpandFileContent(q Q) Q {
	switch s := q.(type) {
	case *Substring:
		if !s.FileName && !s.Content {
			f := *s
			f.FileName = true
			c := *s
			c.Content = true
			return NewOr(&f, &c)
		}
	case *Regexp:
		if !s.FileName && !s.Content {
			f := *s
			f.FileName = true
			c := *s
			c.Content = true
			return NewOr(&f, &c)
		}
	}
	return q
}

// VisitAtoms runs `v` on all atom queries within `q`.
func VisitAtoms(q Q, v func(q Q)) {
	Map(q, func(iQ Q) Q {
		switch iQ.(type) {
		case *And:
		case *Or:
		case *Not:
		case *Type:
		default:
			v(iQ)
		}
		return iQ
	})
}
