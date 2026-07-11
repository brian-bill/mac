// Package bt implements a hand-written lexer and recursive parser for the .bt
// music DSL. It turns raw .bt source into a structured Abstract Syntax Tree
// (a Composition) and reports every problem with a precise 1-based line and
// column, formatted as "file:line:col: message".
//
// The pipeline is a textbook two stages: a rune-scanning Lexer emits a flat
// stream of positioned Tokens, and a token-consuming parser builds the AST.
// This split is what makes column-precise diagnostics possible.
//
// The grammar mirrors GOAL.md §6: a metadata block (key: value, required bpm
// and signature) followed by one or more track blocks ([Header] plus a
// space-separated step-sequencer grid of numbers, dots, and velocity letters).
package bt

import "fmt"

// Position is a 1-based location within the source. Column counts runes, so a
// tab advances the column by one, matching common editor gutter behavior for
// the diagnostics this package emits.
type Position struct {
	Line   int // 1-based line number
	Column int // 1-based column number (counts runes; tab = 1 column)
}

// String renders the position as "line:col", the tail of a compiler-style
// diagnostic such as "drum.bt:4:12".
func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// TokenKind enumerates the lexical categories the lexer produces.
type TokenKind int

const (
	// ILLEGAL is any rune that is not part of the grammar; its Literal carries
	// the offending text so the parser can report it.
	ILLEGAL TokenKind = iota
	// EOF marks the end of input.
	EOF
	// NEWLINE is a single line break ('\n' or a normalized "\r\n"). A run of
	// blank lines yields multiple NEWLINE tokens.
	NEWLINE
	// IDENT is a letter followed by letters, digits, or underscores:
	// metadata keys (bpm, signature), track headers (Kick), and velocity
	// letters (A, a).
	IDENT
	// INT is one or more ASCII digits: a BPM value, a signature number, or a
	// numeric hit in a sequence.
	INT
	// COLON is ':' — the metadata key/value separator.
	COLON
	// SLASH is '/' — the time-signature separator.
	SLASH
	// LBRACKET is '[' — opens a track header.
	LBRACKET
	// RBRACKET is ']' — closes a track header.
	RBRACKET
	// DOT is '.' — a rest in a sequence.
	DOT
)

// String returns a human-readable name for the kind, used in error messages
// and to make test failures legible.
func (k TokenKind) String() string {
	switch k {
	case ILLEGAL:
		return "ILLEGAL"
	case EOF:
		return "EOF"
	case NEWLINE:
		return "NEWLINE"
	case IDENT:
		return "IDENT"
	case INT:
		return "INT"
	case COLON:
		return "COLON"
	case SLASH:
		return "SLASH"
	case LBRACKET:
		return "LBRACKET"
	case RBRACKET:
		return "RBRACKET"
	case DOT:
		return "DOT"
	default:
		return fmt.Sprintf("TokenKind(%d)", int(k))
	}
}

// Token is a single lexeme with its category, exact source text, and the
// position of its first rune.
type Token struct {
	Kind    TokenKind
	Literal string   // exact lexeme text ("120", "Kick", ".", "[")
	Pos     Position // position of the token's first rune
}

// String renders a token for readable test failures, e.g. IDENT("Kick")@4:1.
func (t Token) String() string {
	return fmt.Sprintf("%s(%q)@%s", t.Kind, t.Literal, t.Pos)
}
