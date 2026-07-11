package bt

import "fmt"

// Composition is a whole parsed .bt file: the required metadata plus the
// ordered list of tracks.
type Composition struct {
	BPM       int       // required metadata; always > 0 on a successful parse
	Signature Signature // required metadata
	Tracks    []Track   // in source order
}

// Signature is a time signature such as 4/4.
type Signature struct {
	Numerator   int // beats per bar; > 0
	Denominator int // note value that gets one beat; > 0
}

// String renders the signature as "numerator/denominator", e.g. "4/4".
func (s Signature) String() string {
	return fmt.Sprintf("%d/%d", s.Numerator, s.Denominator)
}

// Track is one [Instrument] block and its subdivision grid.
type Track struct {
	Instrument string   // header ID, e.g. "Kick" (validated syntactically only)
	Steps      []Step   // left-to-right subdivisions
	Pos        Position // position of the '[' that opens the header
}

// StepKind distinguishes a struck subdivision from a silent one.
type StepKind int

const (
	// StepRest is a "." — silence on this subdivision.
	StepRest StepKind = iota
	// StepHit is a struck subdivision: a number ("1") or velocity letter ("A").
	StepHit
)

// String returns a human-readable name for the step kind.
func (k StepKind) String() string {
	switch k {
	case StepRest:
		return "StepRest"
	case StepHit:
		return "StepHit"
	default:
		return fmt.Sprintf("StepKind(%d)", int(k))
	}
}

// Step is a single subdivision cell in a track's grid. Symbol carries the raw
// lexeme exactly as written; interpreting a number's beat or a letter's
// velocity is deferred to the scheduler, which owns those semantics.
type Step struct {
	Kind   StepKind
	Symbol string   // raw lexeme: "." for a rest; "1"/"A" for a hit
	Pos    Position // position of the token that produced this step
}
