package bt

import "unicode/utf8"

// Lexer scans .bt source and produces a stream of positioned tokens via
// repeated calls to Next. It is UTF-8 aware for column counting even though
// the grammar itself is ASCII.
type Lexer struct {
	filename string
	src      []byte
	offset   int // byte offset of the next rune to read
	line     int // 1-based line of the next rune
	column   int // 1-based column of the next rune
}

// NewLexer returns a Lexer positioned at the start of src. filename is used
// only to label diagnostics and need not refer to a real file.
func NewLexer(filename string, src []byte) *Lexer {
	return &Lexer{
		filename: filename,
		src:      src,
		offset:   0,
		line:     1,
		column:   1,
	}
}

// pos returns the current position (of the next rune to be read).
func (l *Lexer) pos() Position {
	return Position{Line: l.line, Column: l.column}
}

// peek returns the rune at the current offset and its byte width without
// consuming it. At end of input it returns (utf8.RuneError, 0).
func (l *Lexer) peek() (rune, int) {
	if l.offset >= len(l.src) {
		return utf8.RuneError, 0
	}
	return utf8.DecodeRune(l.src[l.offset:])
}

// advance consumes one rune, updating the byte offset and line/column. A '\n'
// starts a new line; every other rune advances the column by one.
func (l *Lexer) advance() rune {
	r, w := l.peek()
	if w == 0 {
		return utf8.RuneError
	}
	l.offset += w
	if r == '\n' {
		l.line++
		l.column = 1
	} else {
		l.column++
	}
	return r
}

// Next returns the next token in the stream. Once the input is exhausted it
// returns an EOF token on every call.
func (l *Lexer) Next() Token {
	l.skipSpaces()

	start := l.pos()
	r, w := l.peek()
	if w == 0 {
		return Token{Kind: EOF, Literal: "", Pos: start}
	}

	switch {
	case r == '\n':
		l.advance()
		return Token{Kind: NEWLINE, Literal: "\n", Pos: start}
	case isLetter(r):
		return l.scanIdent(start)
	case isDigit(r):
		return l.scanInt(start)
	case r == ':':
		l.advance()
		return Token{Kind: COLON, Literal: ":", Pos: start}
	case r == '/':
		l.advance()
		return Token{Kind: SLASH, Literal: "/", Pos: start}
	case r == '[':
		l.advance()
		return Token{Kind: LBRACKET, Literal: "[", Pos: start}
	case r == ']':
		l.advance()
		return Token{Kind: RBRACKET, Literal: "]", Pos: start}
	case r == '.':
		l.advance()
		return Token{Kind: DOT, Literal: ".", Pos: start}
	default:
		l.advance()
		return Token{Kind: ILLEGAL, Literal: string(r), Pos: start}
	}
}

// skipSpaces consumes separator whitespace: spaces, tabs, and carriage
// returns. A '\r' immediately followed by '\n' is dropped so that "\r\n"
// yields the single NEWLINE emitted by Next; a lone '\r' is treated as a
// space. Newlines are left for Next to emit as NEWLINE tokens.
func (l *Lexer) skipSpaces() {
	for {
		r, w := l.peek()
		if w == 0 {
			return
		}
		switch r {
		case ' ', '\t', '\r':
			l.advance()
		default:
			return
		}
	}
}

// scanIdent reads a run matching [A-Za-z][A-Za-z0-9_]*, starting at a letter.
func (l *Lexer) scanIdent(start Position) Token {
	begin := l.offset
	for {
		r, w := l.peek()
		if w == 0 || !(isLetter(r) || isDigit(r) || r == '_') {
			break
		}
		l.advance()
	}
	return Token{Kind: IDENT, Literal: string(l.src[begin:l.offset]), Pos: start}
}

// scanInt reads a run of one or more ASCII digits.
func (l *Lexer) scanInt(start Position) Token {
	begin := l.offset
	for {
		r, w := l.peek()
		if w == 0 || !isDigit(r) {
			break
		}
		l.advance()
	}
	return Token{Kind: INT, Literal: string(l.src[begin:l.offset]), Pos: start}
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
