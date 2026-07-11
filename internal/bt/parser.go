package bt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// startPos is where whole-file diagnostics (missing metadata, no tracks) are
// anchored, since they are not tied to any single offending token.
var startPos = Position{Line: 1, Column: 1}

// Parse tokenizes and parses src, returning the composition it describes.
// filename is used only to label diagnostics and need not exist on disk.
//
// On success it returns (*Composition, nil). On failure it returns the
// composition built so far (fields for the parts that parsed) together with a
// non-nil error that is always an ErrorList, so callers can enumerate every
// diagnostic or simply print the first.
func Parse(filename string, src []byte) (*Composition, error) {
	p := newParser(filename, src)
	comp := p.parse()
	if len(p.errs) > 0 {
		return comp, p.errs
	}
	return comp, nil
}

// ParseFile reads the file at path and parses it. The diagnostic filename is
// the base name of path (e.g. "drum.bt"), not the full path.
func ParseFile(path string) (*Composition, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read .bt file %s: %w", path, err)
	}
	return Parse(filepath.Base(path), src)
}

// parser holds the lexer, a one-token lookahead, the composition under
// construction, and the accumulating diagnostics.
type parser struct {
	filename string
	lex      *Lexer
	cur      Token // current token
	peekTok  Token // one-token lookahead

	comp    *Composition
	seenBPM bool
	seenSig bool
	errs    ErrorList
}

func newParser(filename string, src []byte) *parser {
	p := &parser{
		filename: filename,
		lex:      NewLexer(filename, src),
		comp:     &Composition{},
	}
	// Prime cur and peekTok.
	p.next()
	p.next()
	return p
}

// next advances the lookahead: cur becomes the old peek, peek becomes the next
// lexer token.
func (p *parser) next() {
	p.cur = p.peekTok
	p.peekTok = p.lex.Next()
}

// errorf records a positioned diagnostic.
func (p *parser) errorf(pos Position, format string, args ...any) {
	p.errs = append(p.errs, &ParseError{
		File: p.filename,
		Pos:  pos,
		Msg:  fmt.Sprintf(format, args...),
	})
}

// syncToNewline consumes tokens up to and including the next NEWLINE (or up to
// EOF), so parsing can resume cleanly on the following line. It always makes
// progress unless already at EOF.
func (p *parser) syncToNewline() {
	for p.cur.Kind != NEWLINE && p.cur.Kind != EOF {
		p.next()
	}
	if p.cur.Kind == NEWLINE {
		p.next()
	}
}

// skipNewlines consumes any run of blank lines. Surplus blank lines between
// blocks are tolerated (see parse).
func (p *parser) skipNewlines() {
	for p.cur.Kind == NEWLINE {
		p.next()
	}
}

// parse is the top-level entry: metadata block, then track blocks, then the
// whole-file required-content checks.
func (p *parser) parse() *Composition {
	p.skipNewlines()
	p.parseMetadata()

	for {
		p.skipNewlines()
		if p.cur.Kind == EOF {
			break
		}
		if p.cur.Kind == LBRACKET {
			p.parseTrack()
			continue
		}
		// Anything else where a track header was expected: report and resync
		// so we can still find later tracks.
		p.errorf(p.cur.Pos, "expected track header '[', got %s", describe(p.cur))
		p.syncToNewline()
	}

	// Required metadata. Anchored at the start of file because a missing key
	// has no token of its own to point at.
	if !p.seenBPM {
		p.errorf(startPos, "missing required metadata: bpm")
	}
	if !p.seenSig {
		p.errorf(startPos, "missing required metadata: signature")
	}
	// A composition needs at least one track to be renderable.
	if len(p.comp.Tracks) == 0 {
		p.errorf(startPos, "file has no tracks")
	}

	return p.comp
}

// parseMetadata consumes the leading run of "key: value" lines. It stops as
// soon as the next line does not look like a metadata line (e.g. a track
// header), leaving that token for the caller.
func (p *parser) parseMetadata() {
	for p.cur.Kind == IDENT && p.peekTok.Kind == COLON {
		keyTok := p.cur
		p.next() // consume key IDENT
		p.next() // consume ':'

		switch keyTok.Literal {
		case "bpm":
			p.parseBPM(keyTok)
		case "signature":
			p.parseSignature(keyTok)
		default:
			p.errorf(keyTok.Pos, "unknown metadata key %q", keyTok.Literal)
			p.syncToNewline()
		}
	}
}

// parseBPM parses the value of a bpm line; cur is the token after the colon.
func (p *parser) parseBPM(keyTok Token) {
	if p.seenBPM {
		p.errorf(keyTok.Pos, "duplicate metadata key %q", "bpm")
		p.syncToNewline()
		return
	}
	if p.cur.Kind != INT {
		p.errorf(p.cur.Pos, "expected integer for bpm, got %s", describe(p.cur))
		p.syncToNewline()
		return
	}
	v, ok := atoi(p.cur.Literal)
	if !ok {
		p.errorf(p.cur.Pos, "bpm value out of range: %q", p.cur.Literal)
		p.syncToNewline()
		return
	}
	valPos := p.cur.Pos
	p.next() // consume INT
	if v <= 0 {
		p.errorf(valPos, "bpm must be a positive integer")
		p.finishMetaLine()
		return
	}
	p.comp.BPM = v
	p.seenBPM = true
	p.finishMetaLine()
}

// parseSignature parses the value of a signature line (INT '/' INT); cur is the
// token after the colon.
func (p *parser) parseSignature(keyTok Token) {
	if p.seenSig {
		p.errorf(keyTok.Pos, "duplicate metadata key %q", "signature")
		p.syncToNewline()
		return
	}

	if p.cur.Kind != INT {
		p.errorf(p.cur.Pos, "expected integer for signature numerator, got %s", describe(p.cur))
		p.syncToNewline()
		return
	}
	num, ok := atoi(p.cur.Literal)
	numPos := p.cur.Pos
	if !ok {
		p.errorf(numPos, "signature numerator out of range: %q", p.cur.Literal)
		p.syncToNewline()
		return
	}
	p.next() // consume numerator

	if p.cur.Kind != SLASH {
		p.errorf(p.cur.Pos, "expected '/' in signature")
		p.syncToNewline()
		return
	}
	p.next() // consume '/'

	if p.cur.Kind != INT {
		p.errorf(p.cur.Pos, "expected integer for signature denominator, got %s", describe(p.cur))
		p.syncToNewline()
		return
	}
	den, ok := atoi(p.cur.Literal)
	denPos := p.cur.Pos
	if !ok {
		p.errorf(denPos, "signature denominator out of range: %q", p.cur.Literal)
		p.syncToNewline()
		return
	}
	p.next() // consume denominator

	bad := false
	if num <= 0 {
		p.errorf(numPos, "signature numerator must be positive")
		bad = true
	}
	if den <= 0 {
		p.errorf(denPos, "signature denominator must be positive")
		bad = true
	}
	if !bad {
		p.comp.Signature = Signature{Numerator: num, Denominator: den}
		p.seenSig = true
	}
	p.finishMetaLine()
}

// finishMetaLine requires the current metadata line to end here (NEWLINE or
// EOF); trailing tokens on the line are reported and skipped.
func (p *parser) finishMetaLine() {
	switch p.cur.Kind {
	case NEWLINE:
		p.next()
	case EOF:
		// fine
	default:
		p.errorf(p.cur.Pos, "unexpected %s after metadata value", describe(p.cur))
		p.syncToNewline()
	}
}

// parseTrack parses a "[Header]\n sequence" block; cur is the opening '['.
func (p *parser) parseTrack() {
	headerPos := p.cur.Pos
	p.next() // consume '['

	if p.cur.Kind != IDENT {
		p.errorf(p.cur.Pos, "expected instrument name after '['")
		p.syncToNewline()
		return
	}
	name := p.cur.Literal
	p.next() // consume IDENT

	if p.cur.Kind != RBRACKET {
		p.errorf(p.cur.Pos, "expected ']' to close track header")
		p.syncToNewline()
		return
	}
	p.next() // consume ']'

	// The header must occupy its own line.
	switch p.cur.Kind {
	case NEWLINE:
		p.next()
	case EOF:
		p.errorf(headerPos, "track [%s] has no sequence", name)
		return
	default:
		p.errorf(p.cur.Pos, "unexpected %s after track header [%s]", describe(p.cur), name)
		p.syncToNewline()
	}

	steps := p.parseSequence(name, headerPos)
	if steps == nil {
		return
	}
	p.comp.Tracks = append(p.comp.Tracks, Track{
		Instrument: name,
		Steps:      steps,
		Pos:        headerPos,
	})
}

// parseSequence parses the grid line that follows a header. A header with no
// sequence line (blank line or EOF next) is an error. It returns nil when the
// track should be dropped (no valid sequence), or the parsed steps otherwise.
func (p *parser) parseSequence(name string, headerPos Position) []Step {
	if p.cur.Kind == NEWLINE || p.cur.Kind == EOF {
		p.errorf(headerPos, "track [%s] has no sequence", name)
		return nil
	}

	var steps []Step
	for p.cur.Kind != NEWLINE && p.cur.Kind != EOF {
		switch p.cur.Kind {
		case DOT:
			steps = append(steps, Step{Kind: StepRest, Symbol: p.cur.Literal, Pos: p.cur.Pos})
		case INT, IDENT:
			steps = append(steps, Step{Kind: StepHit, Symbol: p.cur.Literal, Pos: p.cur.Pos})
		case ILLEGAL:
			p.errorf(p.cur.Pos, "unexpected character %q in sequence for [%s]", p.cur.Literal, name)
			p.syncToNewline()
			return steps
		default:
			p.errorf(p.cur.Pos, "unexpected %s in sequence for [%s]", describe(p.cur), name)
			p.syncToNewline()
			return steps
		}
		p.next()
	}

	// Consume the line-terminating NEWLINE (EOF is left for the caller).
	if p.cur.Kind == NEWLINE {
		p.next()
	}
	return steps
}

// describe renders a token for an error message: "end of file"/"end of line"
// for the structural tokens, the quoted rune for an illegal one, and
// KIND "literal" otherwise.
func describe(t Token) string {
	switch t.Kind {
	case EOF:
		return "end of file"
	case NEWLINE:
		return "end of line"
	case ILLEGAL:
		return fmt.Sprintf("%q", t.Literal)
	default:
		return fmt.Sprintf("%s %q", t.Kind, t.Literal)
	}
}

// atoi parses a base-10 non-negative integer lexeme. It returns ok=false only
// when the value overflows int (the lexer guarantees the digits themselves).
func atoi(s string) (int, bool) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}
