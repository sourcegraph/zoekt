package syntaxutil

import "regexp/syntax"

// A bunch of aliases to avoid needing to modify parse_test.go too much.

type Regexp = syntax.Regexp

type Op = syntax.Op

const (
	OpNoMatch        = syntax.OpNoMatch
	OpEmptyMatch     = syntax.OpEmptyMatch
	OpLiteral        = syntax.OpLiteral
	OpCharClass      = syntax.OpCharClass
	OpAnyCharNotNL   = syntax.OpAnyCharNotNL
	OpAnyChar        = syntax.OpAnyChar
	OpBeginLine      = syntax.OpBeginLine
	OpEndLine        = syntax.OpEndLine
	OpBeginText      = syntax.OpBeginText
	OpEndText        = syntax.OpEndText
	OpWordBoundary   = syntax.OpWordBoundary
	OpNoWordBoundary = syntax.OpNoWordBoundary
	OpCapture        = syntax.OpCapture
	OpStar           = syntax.OpStar
	OpPlus           = syntax.OpPlus
	OpQuest          = syntax.OpQuest
	OpRepeat         = syntax.OpRepeat
	OpConcat         = syntax.OpConcat
	OpAlternate      = syntax.OpAlternate
)

type Flags = syntax.Flags

const (
	FoldCase      = syntax.FoldCase
	Literal       = syntax.Literal
	ClassNL       = syntax.ClassNL
	DotNL         = syntax.DotNL
	OneLine       = syntax.OneLine
	NonGreedy     = syntax.NonGreedy
	PerlX         = syntax.PerlX
	UnicodeGroups = syntax.UnicodeGroups
	WasDollar     = syntax.WasDollar
	Simple        = syntax.Simple
	MatchNL       = syntax.MatchNL
	Perl          = syntax.Perl
	POSIX         = syntax.POSIX
)

var Parse = syntax.Parse
