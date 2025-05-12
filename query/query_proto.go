package query

import (
	"fmt"
	"regexp/syntax"

	"github.com/RoaringBitmap/roaring"
	"github.com/grafana/regexp"

	webserverv1 "github.com/sourcegraph/zoekt/grpc/protos/zoekt/webserver/v1"
)

func QToProto(q Q) *webserverv1.Q {
	switch v := q.(type) {
	case RawConfig:
		return &webserverv1.Q{Query: &webserverv1.Q_RawConfig{RawConfig: v.ToProto()}}
	case *Regexp:
		return &webserverv1.Q{Query: &webserverv1.Q_Regexp{Regexp: v.ToProto()}}
	case *Symbol:
		return &webserverv1.Q{Query: &webserverv1.Q_Symbol{Symbol: v.ToProto()}}
	case *Language:
		return &webserverv1.Q{Query: &webserverv1.Q_Language{Language: v.ToProto()}}
	case *Const:
		return &webserverv1.Q{Query: &webserverv1.Q_Const{Const: v.Value}}
	case *Repo:
		return &webserverv1.Q{Query: &webserverv1.Q_Repo{Repo: v.ToProto()}}
	case *RepoRegexp:
		return &webserverv1.Q{Query: &webserverv1.Q_RepoRegexp{RepoRegexp: v.ToProto()}}
	case *BranchesRepos:
		return &webserverv1.Q{Query: &webserverv1.Q_BranchesRepos{BranchesRepos: v.ToProto()}}
	case *RepoIDs:
		return &webserverv1.Q{Query: &webserverv1.Q_RepoIds{RepoIds: v.ToProto()}}
	case *RepoSet:
		return &webserverv1.Q{Query: &webserverv1.Q_RepoSet{RepoSet: v.ToProto()}}
	case *FileNameSet:
		return &webserverv1.Q{Query: &webserverv1.Q_FileNameSet{FileNameSet: v.ToProto()}}
	case *Type:
		return &webserverv1.Q{Query: &webserverv1.Q_Type{Type: v.ToProto()}}
	case *Substring:
		return &webserverv1.Q{Query: &webserverv1.Q_Substring{Substring: v.ToProto()}}
	case *And:
		return &webserverv1.Q{Query: &webserverv1.Q_And{And: v.ToProto()}}
	case *Or:
		return &webserverv1.Q{Query: &webserverv1.Q_Or{Or: v.ToProto()}}
	case *Not:
		return &webserverv1.Q{Query: &webserverv1.Q_Not{Not: v.ToProto()}}
	case *Branch:
		return &webserverv1.Q{Query: &webserverv1.Q_Branch{Branch: v.ToProto()}}
	case *Boost:
		return &webserverv1.Q{Query: &webserverv1.Q_Boost{Boost: v.ToProto()}}
	default:
		// The following nodes do not have a proto representation:
		// - caseQ: only used internally, not by the RPC layer
		panic(fmt.Sprintf("unknown query node %T", v))
	}
}

func QFromProto(p *webserverv1.Q) (Q, error) {
	switch v := p.Query.(type) {
	case *webserverv1.Q_RawConfig:
		return RawConfigFromProto(v.RawConfig), nil
	case *webserverv1.Q_Regexp:
		return RegexpFromProto(v.Regexp)
	case *webserverv1.Q_Symbol:
		return SymbolFromProto(v.Symbol)
	case *webserverv1.Q_Language:
		return LanguageFromProto(v.Language), nil
	case *webserverv1.Q_Const:
		return &Const{Value: v.Const}, nil
	case *webserverv1.Q_Repo:
		return RepoFromProto(v.Repo)
	case *webserverv1.Q_RepoRegexp:
		return RepoRegexpFromProto(v.RepoRegexp)
	case *webserverv1.Q_BranchesRepos:
		return BranchesReposFromProto(v.BranchesRepos)
	case *webserverv1.Q_RepoIds:
		return RepoIDsFromProto(v.RepoIds)
	case *webserverv1.Q_RepoSet:
		return RepoSetFromProto(v.RepoSet), nil
	case *webserverv1.Q_FileNameSet:
		return FileNameSetFromProto(v.FileNameSet), nil
	case *webserverv1.Q_Type:
		return TypeFromProto(v.Type)
	case *webserverv1.Q_Substring:
		return SubstringFromProto(v.Substring), nil
	case *webserverv1.Q_And:
		return AndFromProto(v.And)
	case *webserverv1.Q_Or:
		return OrFromProto(v.Or)
	case *webserverv1.Q_Not:
		return NotFromProto(v.Not)
	case *webserverv1.Q_Branch:
		return BranchFromProto(v.Branch), nil
	case *webserverv1.Q_Boost:
		return BoostFromProto(v.Boost)
	default:
		panic(fmt.Sprintf("unknown query node %T", p.Query))
	}
}

func RegexpFromProto(p *webserverv1.Regexp) (*Regexp, error) {
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

func (r *Regexp) ToProto() *webserverv1.Regexp {
	return &webserverv1.Regexp{
		Regexp:        r.Regexp.String(),
		FileName:      r.FileName,
		Content:       r.Content,
		CaseSensitive: r.CaseSensitive,
	}
}

func SymbolFromProto(p *webserverv1.Symbol) (*Symbol, error) {
	expr, err := QFromProto(p.GetExpr())
	if err != nil {
		return nil, err
	}

	return &Symbol{
		Expr: expr,
	}, nil
}

func (s *Symbol) ToProto() *webserverv1.Symbol {
	return &webserverv1.Symbol{
		Expr: QToProto(s.Expr),
	}
}

func LanguageFromProto(p *webserverv1.Language) *Language {
	return &Language{
		Language: p.GetLanguage(),
	}
}

func (l *Language) ToProto() *webserverv1.Language {
	return &webserverv1.Language{Language: l.Language}
}

func RepoFromProto(p *webserverv1.Repo) (*Repo, error) {
	r, err := regexp.Compile(p.GetRegexp())
	if err != nil {
		return nil, err
	}
	return &Repo{
		Regexp: r,
	}, nil
}

func (q *Repo) ToProto() *webserverv1.Repo {
	return &webserverv1.Repo{
		Regexp: q.Regexp.String(),
	}
}

func RepoRegexpFromProto(p *webserverv1.RepoRegexp) (*RepoRegexp, error) {
	r, err := regexp.Compile(p.GetRegexp())
	if err != nil {
		return nil, err
	}
	return &RepoRegexp{
		Regexp: r,
	}, nil
}

func (q *RepoRegexp) ToProto() *webserverv1.RepoRegexp {
	return &webserverv1.RepoRegexp{
		Regexp: q.Regexp.String(),
	}
}

func BranchesReposFromProto(p *webserverv1.BranchesRepos) (*BranchesRepos, error) {
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

func (br *BranchesRepos) ToProto() *webserverv1.BranchesRepos {
	list := make([]*webserverv1.BranchRepos, len(br.List))
	for i, branchRepo := range br.List {
		list[i] = branchRepo.ToProto()
	}

	return &webserverv1.BranchesRepos{
		List: list,
	}
}

func RepoIDsFromProto(p *webserverv1.RepoIds) (*RepoIDs, error) {
	bm := roaring.NewBitmap()
	err := bm.UnmarshalBinary(p.GetRepos())
	if err != nil {
		return nil, err
	}

	return &RepoIDs{
		Repos: bm,
	}, nil
}

func (q *RepoIDs) ToProto() *webserverv1.RepoIds {
	b, err := q.Repos.ToBytes()
	if err != nil {
		panic("unexpected error marshalling bitmap: " + err.Error())
	}
	return &webserverv1.RepoIds{
		Repos: b,
	}
}

func BranchReposFromProto(p *webserverv1.BranchRepos) (BranchRepos, error) {
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

func (br *BranchRepos) ToProto() *webserverv1.BranchRepos {
	b, err := br.Repos.ToBytes()
	if err != nil {
		panic("unexpected error marshalling bitmap: " + err.Error())
	}

	return &webserverv1.BranchRepos{
		Branch: br.Branch,
		Repos:  b,
	}
}

func RepoSetFromProto(p *webserverv1.RepoSet) *RepoSet {
	return &RepoSet{
		Set: p.GetSet(),
	}
}

func (q *RepoSet) ToProto() *webserverv1.RepoSet {
	return &webserverv1.RepoSet{
		Set: q.Set,
	}
}

func FileNameSetFromProto(p *webserverv1.FileNameSet) *FileNameSet {
	m := make(map[string]struct{}, len(p.GetSet()))
	for _, name := range p.GetSet() {
		m[name] = struct{}{}
	}
	return &FileNameSet{
		Set: m,
	}
}

func (q *FileNameSet) ToProto() *webserverv1.FileNameSet {
	s := make([]string, 0, len(q.Set))
	for name := range q.Set {
		s = append(s, name)
	}
	return &webserverv1.FileNameSet{
		Set: s,
	}
}

func TypeFromProto(p *webserverv1.Type) (*Type, error) {
	child, err := QFromProto(p.GetChild())
	if err != nil {
		return nil, err
	}

	var kind uint8
	switch p.GetType() {
	case webserverv1.Type_KIND_FILE_MATCH:
		kind = TypeFileMatch
	case webserverv1.Type_KIND_FILE_NAME:
		kind = TypeFileName
	case webserverv1.Type_KIND_REPO:
		kind = TypeRepo
	}

	return &Type{
		Child: child,
		// TODO: make proper enum types
		Type: kind,
	}, nil
}

func (q *Type) ToProto() *webserverv1.Type {
	var kind webserverv1.Type_Kind
	switch q.Type {
	case TypeFileMatch:
		kind = webserverv1.Type_KIND_FILE_MATCH
	case TypeFileName:
		kind = webserverv1.Type_KIND_FILE_NAME
	case TypeRepo:
		kind = webserverv1.Type_KIND_REPO
	}

	return &webserverv1.Type{
		Child: QToProto(q.Child),
		Type:  kind,
	}
}

func SubstringFromProto(p *webserverv1.Substring) *Substring {
	return &Substring{
		Pattern:       p.GetPattern(),
		CaseSensitive: p.GetCaseSensitive(),
		FileName:      p.GetFileName(),
		Content:       p.GetContent(),
	}
}

func (q *Substring) ToProto() *webserverv1.Substring {
	return &webserverv1.Substring{
		Pattern:       q.Pattern,
		CaseSensitive: q.CaseSensitive,
		FileName:      q.FileName,
		Content:       q.Content,
	}
}

func OrFromProto(p *webserverv1.Or) (*Or, error) {
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

func (q *Or) ToProto() *webserverv1.Or {
	children := make([]*webserverv1.Q, len(q.Children))
	for i, child := range q.Children {
		children[i] = QToProto(child)
	}
	return &webserverv1.Or{
		Children: children,
	}
}

func BoostFromProto(p *webserverv1.Boost) (*Boost, error) {
	child, err := QFromProto(p.GetChild())
	if err != nil {
		return nil, err
	}
	return &Boost{
		Child: child,
		Boost: p.GetBoost(),
	}, nil
}

func (q *Boost) ToProto() *webserverv1.Boost {
	return &webserverv1.Boost{
		Child: QToProto(q.Child),
		Boost: q.Boost,
	}
}

func NotFromProto(p *webserverv1.Not) (*Not, error) {
	child, err := QFromProto(p.GetChild())
	if err != nil {
		return nil, err
	}
	return &Not{
		Child: child,
	}, nil
}

func (q *Not) ToProto() *webserverv1.Not {
	return &webserverv1.Not{
		Child: QToProto(q.Child),
	}
}

func AndFromProto(p *webserverv1.And) (*And, error) {
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

func (q *And) ToProto() *webserverv1.And {
	children := make([]*webserverv1.Q, len(q.Children))
	for i, child := range q.Children {
		children[i] = QToProto(child)
	}
	return &webserverv1.And{
		Children: children,
	}
}

func BranchFromProto(p *webserverv1.Branch) *Branch {
	return &Branch{
		Pattern: p.GetPattern(),
		Exact:   p.GetExact(),
	}
}

func (q *Branch) ToProto() *webserverv1.Branch {
	return &webserverv1.Branch{
		Pattern: q.Pattern,
		Exact:   q.Exact,
	}
}

func RawConfigFromProto(p *webserverv1.RawConfig) (res RawConfig) {
	for _, protoFlag := range p.Flags {
		switch protoFlag {
		case webserverv1.RawConfig_FLAG_ONLY_PUBLIC:
			res |= RcOnlyPublic
		case webserverv1.RawConfig_FLAG_ONLY_PRIVATE:
			res |= RcOnlyPrivate
		case webserverv1.RawConfig_FLAG_ONLY_FORKS:
			res |= RcOnlyForks
		case webserverv1.RawConfig_FLAG_NO_FORKS:
			res |= RcNoForks
		case webserverv1.RawConfig_FLAG_ONLY_ARCHIVED:
			res |= RcOnlyArchived
		case webserverv1.RawConfig_FLAG_NO_ARCHIVED:
			res |= RcNoArchived
		}
	}
	return res
}

func (r RawConfig) ToProto() *webserverv1.RawConfig {
	var flags []webserverv1.RawConfig_Flag
	for _, flag := range flagNames {
		if r&flag.Mask != 0 {
			switch flag.Mask {
			case RcOnlyPublic:
				flags = append(flags, webserverv1.RawConfig_FLAG_ONLY_PUBLIC)
			case RcOnlyPrivate:
				flags = append(flags, webserverv1.RawConfig_FLAG_ONLY_PRIVATE)
			case RcOnlyForks:
				flags = append(flags, webserverv1.RawConfig_FLAG_ONLY_FORKS)
			case RcNoForks:
				flags = append(flags, webserverv1.RawConfig_FLAG_NO_FORKS)
			case RcOnlyArchived:
				flags = append(flags, webserverv1.RawConfig_FLAG_ONLY_ARCHIVED)
			case RcNoArchived:
				flags = append(flags, webserverv1.RawConfig_FLAG_NO_ARCHIVED)
			}
		}
	}
	return &webserverv1.RawConfig{Flags: flags}
}
