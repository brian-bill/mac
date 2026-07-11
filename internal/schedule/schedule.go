package schedule

import (
	"fmt"
	"sort"

	"github.com/bryanbill/mac/internal/bt"
)

// startPos anchors whole-composition diagnostics (nil composition, non-positive
// metadata) that are not tied to any single token, matching the parser's
// convention of pointing such errors at the start of file.
var startPos = bt.Position{Line: 1, Column: 1}

// Schedule flattens comp into a chronologically sorted event slice.
//
// filename labels any diagnostics (file:line:col), matching Spec 003; it need
// not exist on disk. vel resolves velocity letters and may be nil when the
// composition contains none.
//
// Timing math (all milliseconds, t0 = 0 at the start of the bar):
//
//	beatMs  = 60000 / BPM                  // one denominator-note beat
//	barMs   = Signature.Numerator * beatMs // one full bar
//	stepMs  = barMs / len(track.Steps)     // even subdivision across the grid
//	time(i) = i * stepMs                   // i = 0-based step index
//
// Each track's grid fills exactly one bar, so tracks of differing lengths stay
// bar-synchronized and all start together at t = 0.
//
// On success returns (events, nil) — never nil for a valid composition (an
// empty composition yields a non-nil, len-0 slice). On failure returns the
// events scheduled so far together with a non-nil error that is a bt.ErrorList
// (unresolved velocity letters and the defensive guards below). Recovery is
// collect-all: a bad velocity symbol or an empty track is recorded and skipped,
// and scheduling continues.
func Schedule(filename string, comp *bt.Composition, vel VelocityFunc) ([]AudioEvent, error) {
	var errs bt.ErrorList
	errorf := func(pos bt.Position, format string, args ...any) {
		errs = append(errs, &bt.ParseError{File: filename, Pos: pos, Msg: fmt.Sprintf(format, args...)})
	}

	if comp == nil {
		errorf(startPos, "cannot schedule: nil composition")
		return nil, errs
	}

	// Defensive metadata guards. The parser guarantees these on a successful
	// parse, but Schedule may receive a partial AST or be called directly in
	// tests; validating here keeps the bar math divide-by-zero-free.
	if comp.BPM <= 0 {
		errorf(startPos, "cannot schedule: bpm must be positive, got %d", comp.BPM)
	}
	if comp.Signature.Numerator <= 0 {
		errorf(startPos, "cannot schedule: signature numerator must be positive, got %d", comp.Signature.Numerator)
	}
	if comp.Signature.Denominator <= 0 {
		errorf(startPos, "cannot schedule: signature denominator must be positive, got %d", comp.Signature.Denominator)
	}
	if len(errs) > 0 {
		return nil, errs
	}

	bar := barMs(comp)

	// events is non-nil so a valid-but-silent composition returns a len-0
	// slice, not nil (per the documented contract).
	events := make([]AudioEvent, 0)
	for _, track := range comp.Tracks {
		if len(track.Steps) == 0 {
			// Belt-and-suspenders: Spec 003 already rejects empty sequences.
			errorf(track.Pos, "track [%s] has no steps to schedule", track.Instrument)
			continue
		}
		stepMs := bar / float64(len(track.Steps))
		for i, step := range track.Steps {
			if step.Kind == bt.StepRest {
				continue
			}
			gain, ok := resolveVelocity(track.Instrument, step.Symbol, vel)
			if !ok {
				errorf(step.Pos, "%s", velocityErrMsg(track.Instrument, step.Symbol, vel))
				continue
			}
			events = append(events, AudioEvent{
				TimeMs:     float64(i) * stepMs,
				Instrument: track.Instrument,
				Velocity:   gain,
			})
		}
	}

	// Total order: TimeMs, then track index, then step index. Tracks are walked
	// in source order and steps left-to-right, so the append order above already
	// encodes the (trackIdx, stepIdx) tie-break; a stable sort on TimeMs alone
	// therefore yields the full, byte-for-byte reproducible ordering.
	sort.SliceStable(events, func(a, b int) bool {
		return events[a].TimeMs < events[b].TimeMs
	})

	if len(errs) > 0 {
		return events, errs
	}
	return events, nil
}

// barMs returns the duration of one bar in milliseconds. Callers must ensure
// BPM and Numerator are positive (Schedule's guards do this).
func barMs(comp *bt.Composition) float64 {
	beatMs := 60000.0 / float64(comp.BPM)
	return float64(comp.Signature.Numerator) * beatMs
}

// resolveVelocity maps a hit's raw symbol to a gain. Numeric hits are
// full-velocity beat markers (1.0); letter hits defer to vel. ok is false for
// an unresolved letter (unknown symbol, or a nil vel), which the caller turns
// into a positioned diagnostic. resolveVelocity is only called for StepHit
// steps, so the symbol is never a rest ".".
func resolveVelocity(instrument, symbol string, vel VelocityFunc) (gain float64, ok bool) {
	if isNumeric(symbol) {
		return 1.0, true
	}
	if vel == nil {
		return 0, false
	}
	return vel(instrument, symbol)
}

// velocityErrMsg distinguishes "no map available" (nil resolver) from "unknown
// symbol" (resolver said no) so the diagnostic points at the real cause.
func velocityErrMsg(instrument, symbol string, vel VelocityFunc) string {
	if vel == nil {
		return fmt.Sprintf("no velocity map available for symbol %q", symbol)
	}
	return fmt.Sprintf("unknown velocity symbol %q for instrument %q", symbol, instrument)
}

// isNumeric reports whether s is one or more ASCII digits. The empty string is
// not numeric. No strconv needed: a symbol is a beat marker iff every rune is a
// digit (the lexer already guarantees INT lexemes, but letter symbols such as
// "A" must fall through to the resolver).
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
