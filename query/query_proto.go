package query

import (
	"fmt"
	"regexp/syntax"

	"github.com/RoaringBitmap/roaring"
	"github.com/grafana/regexp"
	v1 "github.com/sourcegraph/zoekt/grpc/v1"
)

func QToProto(q Q) *v1.Q {
	switch v := q.(type) {
	case RawConfig:
		return &v1.Q{Query: &v1.Q_RawConfig{RawConfig: v.ToProto()}}
	case *Regexp:
		return &v1.Q{Query: &v1.Q_Regexp{Regexp: v.ToProto()}}
	case *Symbol:
		return &v1.Q{Query: &v1.Q_Symbol{Symbol: v.ToProto()}}
	case *Language:
		return &v1.Q{Query: &v1.Q_Language{Language: v.ToProto()}}
	case *Const:
		return &v1.Q{Query: &v1.Q_Const{Const: v.ToProto()}}
	case *Repo:
		return &v1.Q{Query: &v1.Q_Repo{Repo: v.ToProto()}}
	case *RepoRegexp:
		return &v1.Q{Query: &v1.Q_RepoRegexp{RepoRegexp: v.ToProto()}}
	case *BranchesRepos:
		return &v1.Q{Query: &v1.Q_BranchesRepos{BranchesRepos: v.ToProto()}}
	case *RepoIDs:
		return &v1.Q{Query: &v1.Q_RepoIds{RepoIds: v.ToProto()}}
	case *RepoSet:
		return &v1.Q{Query: &v1.Q_RepoSet{RepoSet: v.ToProto()}}
	case *FileNameSet:
		return &v1.Q{Query: &v1.Q_FileNameSet{FileNameSet: v.ToProto()}}
	case *Type:
		return &v1.Q{Query: &v1.Q_Type{Type: v.ToProto()}}
	case *Substring:
		return &v1.Q{Query: &v1.Q_Substring{Substring: v.ToProto()}}
	case *And:
		return &v1.Q{Query: &v1.Q_And{And: v.ToProto()}}
	case *Or:
		return &v1.Q{Query: &v1.Q_Or{Or: v.ToProto()}}
	case *Not:
		return &v1.Q{Query: &v1.Q_Not{Not: v.ToProto()}}
	case *Branch:
		return &v1.Q{Query: &v1.Q_Branch{Branch: v.ToProto()}}
	default:
		// The following nodes do not have a proto representation:
		// - GobCache: only needed for Gob encoding
		// - caseQ: only used internally, not by the RPC layer
		panic(fmt.Sprintf("unknown query node %T", v))
	}
}

func QFromProto(p *v1.Q) (Q, error) {
	switch v := p.Query.(type) {
	case *v1.Q_RawConfig:
		return RawConfigFromProto(v.RawConfig), nil
	case *v1.Q_Regexp:
		return RegexpFromProto(v.Regexp)
	case *v1.Q_Symbol:
		return SymbolFromProto(v.Symbol)
	case *v1.Q_Language:
		return LanguageFromProto(v.Language), nil
	case *v1.Q_Const:
		return ConstFromProto(v.Const), nil
	case *v1.Q_Repo:
		return RepoFromProto(v.Repo)
	case *v1.Q_RepoRegexp:
		return RepoRegexpFromProto(v.RepoRegexp)
	case *v1.Q_BranchesRepos:
		return BranchesReposFromProto(v.BranchesRepos)
	case *v1.Q_RepoIds:
		return RepoIDsFromProto(v.RepoIds)
	case *v1.Q_RepoSet:
		return RepoSetFromProto(v.RepoSet), nil
	case *v1.Q_FileNameSet:
		return FileNameSetFromProto(v.FileNameSet), nil
	case *v1.Q_Type:
		return TypeFromProto(v.Type)
	case *v1.Q_Substring:
		return SubstringFromProto(v.Substring), nil
	case *v1.Q_And:
		return AndFromProto(v.And)
	case *v1.Q_Or:
		return OrFromProto(v.Or)
	case *v1.Q_Not:
		return NotFromProto(v.Not)
	case *v1.Q_Branch:
		return BranchFromProto(v.Branch), nil
	default:
		panic(fmt.Sprintf("unknown query node %T", p.Query))
	}
}

func RegexpFromProto(p *v1.Regexp) (*Regexp, error) {
	parsed, err := syntax.Parse(p.GetRegexp(), regexpFlags)
	if err != nil {
		return nil, err
	}
	return &Regexp{
		Regexp:        parsed,
		FileName:      p.GetFileName(),
		Content:       p.GetContent(),
		CaseSensitive: p.GetCaseSensitive(),
	}, nil
}

func (r *Regexp) ToProto() *v1.Regexp {
	return &v1.Regexp{
		Regexp:        r.Regexp.String(),
		FileName:      r.FileName,
		Content:       r.Content,
		CaseSensitive: r.CaseSensitive,
	}
}

func SymbolFromProto(p *v1.Symbol) (*Symbol, error) {
	expr, err := QFromProto(p.GetExpr())
	if err != nil {
		return nil, err
	}

	return &Symbol{
		Expr: expr,
	}, nil
}

func (s *Symbol) ToProto() *v1.Symbol {
	return &v1.Symbol{
		Expr: QToProto(s.Expr),
	}
}

func LanguageFromProto(p *v1.Language) *Language {
	return &Language{
		Language: p.GetLanguage(),
	}
}

func (l *Language) ToProto() *v1.Language {
	return &v1.Language{Language: l.Language}
}

func ConstFromProto(p *v1.Const) *Const {
	return &Const{
		Value: p.GetValue(),
	}
}

func (q *Const) ToProto() *v1.Const {
	return &v1.Const{Value: q.Value}
}

func RepoFromProto(p *v1.Repo) (*Repo, error) {
	r, err := regexp.Compile(p.GetRegexp())
	if err != nil {
		return nil, err
	}
	return &Repo{
		Regexp: r,
	}, nil
}

func (q *Repo) ToProto() *v1.Repo {
	return &v1.Repo{
		Regexp: q.Regexp.String(),
	}
}

func RepoRegexpFromProto(p *v1.RepoRegexp) (*RepoRegexp, error) {
	r, err := regexp.Compile(p.GetRegexp())
	if err != nil {
		return nil, err
	}
	return &RepoRegexp{
		Regexp: r,
	}, nil
}

func (q *RepoRegexp) ToProto() *v1.RepoRegexp {
	return &v1.RepoRegexp{
		Regexp: q.Regexp.String(),
	}
}

func BranchesReposFromProto(p *v1.BranchesRepos) (*BranchesRepos, error) {
	brs := make([]BranchRepos, len(p.GetList()))
	for i, br := range p.GetList() {
		branchRepos, err := BranchReposFromProto(br)
		if err != nil {
			return nil, err
		}
		brs[i] = branchRepos
	}
	return &BranchesRepos{
		List: brs,
	}, nil
}

func (br *BranchesRepos) ToProto() *v1.BranchesRepos {
	list := make([]*v1.BranchRepos, len(br.List))
	for i, branchRepo := range br.List {
		list[i] = branchRepo.ToProto()
	}

	return &v1.BranchesRepos{
		List: list,
	}
}

func RepoIDsFromProto(p *v1.RepoIds) (*RepoIDs, error) {
	bm := roaring.NewBitmap()
	err := bm.UnmarshalBinary(p.GetRepos())
	if err != nil {
		return nil, err
	}

	return &RepoIDs{
		Repos: bm,
	}, nil
}

func (q *RepoIDs) ToProto() *v1.RepoIds {
	b, err := q.Repos.ToBytes()
	if err != nil {
		panic("unexpected error marshalling bitmap: " + err.Error())
	}
	return &v1.RepoIds{
		Repos: b,
	}
}

func BranchReposFromProto(p *v1.BranchRepos) (BranchRepos, error) {
	bm := roaring.NewBitmap()
	err := bm.UnmarshalBinary(p.GetRepos())
	if err != nil {
		return BranchRepos{}, err
	}
	return BranchRepos{
		Branch: p.GetBranch(),
		Repos:  bm,
	}, nil
}

func (br *BranchRepos) ToProto() *v1.BranchRepos {
	b, err := br.Repos.ToBytes()
	if err != nil {
		panic("unexpected error marshalling bitmap: " + err.Error())
	}

	return &v1.BranchRepos{
		Branch: br.Branch,
		Repos:  b,
	}
}

func RepoSetFromProto(p *v1.RepoSet) *RepoSet {
	return &RepoSet{
		Set: p.GetSet(),
	}
}

func (q *RepoSet) ToProto() *v1.RepoSet {
	return &v1.RepoSet{
		Set: q.Set,
	}
}

func FileNameSetFromProto(p *v1.FileNameSet) *FileNameSet {
	m := make(map[string]struct{}, len(p.GetSet()))
	for _, name := range p.GetSet() {
		m[name] = struct{}{}
	}
	return &FileNameSet{
		Set: m,
	}
}

func (q *FileNameSet) ToProto() *v1.FileNameSet {
	s := make([]string, 0, len(q.Set))
	for name := range q.Set {
		s = append(s, name)
	}
	return &v1.FileNameSet{
		Set: s,
	}
}

func TypeFromProto(p *v1.Type) (*Type, error) {
	child, err := QFromProto(p.GetChild())
	if err != nil {
		return nil, err
	}

	return &Type{
		Child: child,
		// TODO: make proper enum types
		Type: uint8(p.GetType()),
	}, nil
}

func (q *Type) ToProto() *v1.Type {
	return &v1.Type{
		Child: QToProto(q.Child),
		Type:  uint32(q.Type),
	}
}

func SubstringFromProto(p *v1.Substring) *Substring {
	return &Substring{
		Pattern:       p.GetPattern(),
		CaseSensitive: p.GetCaseSensitive(),
		FileName:      p.GetFileName(),
		Content:       p.GetContent(),
	}
}

func (q *Substring) ToProto() *v1.Substring {
	return &v1.Substring{
		Pattern:       q.Pattern,
		CaseSensitive: q.CaseSensitive,
		FileName:      q.FileName,
		Content:       q.Content,
	}
}

func OrFromProto(p *v1.Or) (*Or, error) {
	children := make([]Q, len(p.GetChildren()))
	for i, child := range p.GetChildren() {
		c, err := QFromProto(child)
		if err != nil {
			return nil, err
		}
		children[i] = c
	}
	return &Or{
		Children: children,
	}, nil
}

func (q *Or) ToProto() *v1.Or {
	children := make([]*v1.Q, len(q.Children))
	for i, child := range q.Children {
		children[i] = QToProto(child)
	}
	return &v1.Or{
		Children: children,
	}
}

func NotFromProto(p *v1.Not) (*Not, error) {
	child, err := QFromProto(p.GetChild())
	if err != nil {
		return nil, err
	}
	return &Not{
		Child: child,
	}, nil
}

func (q *Not) ToProto() *v1.Not {
	return &v1.Not{
		Child: QToProto(q.Child),
	}
}

func AndFromProto(p *v1.And) (*And, error) {
	children := make([]Q, len(p.GetChildren()))
	for i, child := range p.GetChildren() {
		c, err := QFromProto(child)
		if err != nil {
			return nil, err
		}
		children[i] = c
	}
	return &And{
		Children: children,
	}, nil
}

func (q *And) ToProto() *v1.And {
	children := make([]*v1.Q, len(q.Children))
	for i, child := range q.Children {
		children[i] = QToProto(child)
	}
	return &v1.And{
		Children: children,
	}
}

func BranchFromProto(p *v1.Branch) *Branch {
	return &Branch{
		Pattern: p.GetPattern(),
		Exact:   p.GetExact(),
	}
}

func (q *Branch) ToProto() *v1.Branch {
	return &v1.Branch{
		Pattern: q.Pattern,
		Exact:   q.Exact,
	}
}

func RawConfigFromProto(p *v1.RawConfig) RawConfig {
	return RawConfig(p.Flags)
}

func (r RawConfig) ToProto() *v1.RawConfig {
	return &v1.RawConfig{Flags: uint64(r)}
}
