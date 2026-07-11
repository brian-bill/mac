// Package schedule flattens a parsed .bt Composition (from package bt) into a
// flat, chronologically sorted slice of AudioEvents — the intermediate
// representation the audio engine (a later spec) turns into mixed buffers.
//
// It is the single source of timing truth for both the CLI and the Web IDE, so
// all BPM/signature math and event ordering live here and are computed with a
// fixed, platform-independent formula (see Schedule). The package is DB-free:
// it imports only internal/bt and the standard library, and receives the
// instrument velocity map injected as a VelocityFunc rather than reaching into
// the registry itself.
package schedule

// AudioEvent is one scheduled instrument strike: an instrument fired at an
// exact time with a resolved velocity. Field order mirrors the
// {Time, Instrument, Velocity} shape named in INSTRUCTIONS.md §1.
type AudioEvent struct {
	TimeMs     float64 // milliseconds from the start of the bar (t0 = 0)
	Instrument string  // track header ID, e.g. "Kick"
	Velocity   float64 // 0.0–1.0 gain; 1.0 for numeric hits
}

// VelocityFunc resolves a velocity letter symbol (e.g. "A", "a") for a given
// instrument into a 0.0–1.0 gain. ok is false when the symbol is unknown for
// that instrument, which the scheduler turns into a positioned error.
//
// It is only consulted for letter hits; numeric hits never call it. A nil
// VelocityFunc is legal for compositions that contain no letter hits.
type VelocityFunc func(instrument, symbol string) (gain float64, ok bool)
