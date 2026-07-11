package bt

import (
	"fmt"
	"strings"
)

// ParseError is a single positioned diagnostic. Its Error string follows the
// Go compiler convention "file:line:col: message", e.g. "drum.bt:4:12: ...".
type ParseError struct {
	File string // filename used for diagnostics, e.g. "drum.bt"
	Pos  Position
	Msg  string
}

// Error renders the diagnostic as "file:line:col: message".
func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%s: %s", e.File, e.Pos, e.Msg)
}

// ErrorList aggregates every ParseError collected during a single parse. The
// parser recovers at line granularity so one malformed line does not hide the
// rest, which serves the Web IDE's need to display all errors at once.
type ErrorList []*ParseError

// Error renders the first diagnostic and, when there is more than one, a
// "(and N more)" summary. Use the slice directly to enumerate every error.
func (el ErrorList) Error() string {
	switch len(el) {
	case 0:
		return "no errors"
	case 1:
		return el[0].Error()
	default:
		var b strings.Builder
		b.WriteString(el[0].Error())
		fmt.Fprintf(&b, " (and %d more)", len(el)-1)
		return b.String()
	}
}
