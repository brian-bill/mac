package schedule

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/bryanbill/mac/internal/bt"
)

// hit builds a StepHit with the given symbol at a throwaway position.
func hit(sym string) bt.Step {
	return bt.Step{Kind: bt.StepHit, Symbol: sym, Pos: bt.Position{Line: 1, Column: 1}}
}

// hitAt builds a StepHit carrying an explicit position (for column assertions).
func hitAt(sym string, line, col int) bt.Step {
	return bt.Step{Kind: bt.StepHit, Symbol: sym, Pos: bt.Position{Line: line, Column: col}}
}

// rest builds a StepRest.
func rest() bt.Step {
	return bt.Step{Kind: bt.StepRest, Symbol: ".", Pos: bt.Position{Line: 1, Column: 1}}
}

// grid builds a track whose steps come from a compact spec string: each rune is
// either '.' (rest) or a digit (numeric hit). Handy for timing tables.
func grid(instrument, spec string) bt.Track {
	steps := make([]bt.Step, 0, len(spec))
	for _, r := range spec {
		if r == '.' {
			steps = append(steps, rest())
		} else {
			steps = append(steps, hit(string(r)))
		}
	}
	return bt.Track{Instrument: instrument, Steps: steps, Pos: bt.Position{Line: 1, Column: 1}}
}

// canonicalDrumBt is the GOAL.md §6 example: 120 bpm, 4/4, three 16-step tracks.
func canonicalDrumBt() *bt.Composition {
	return &bt.Composition{
		BPM:       120,
		Signature: bt.Signature{Numerator: 4, Denominator: 4},
		Tracks: []bt.Track{
			grid("Kick", "1...1...1...1..."),
			grid("Snare", "....1.......1..."),
			grid("HiHat", "1.1.1.1.1.1.1.1."),
		},
	}
}

const epsilon = 1e-9

func TestSchedule_CanonicalDrumBt(t *testing.T) {
	events, err := Schedule("drum.bt", canonicalDrumBt(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 4 Kick + 2 Snare + 8 HiHat = 14 events.
	want := []AudioEvent{
		{TimeMs: 0, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 0, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 250, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 500, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 500, Instrument: "Snare", Velocity: 1.0},
		{TimeMs: 500, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 750, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 1000, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 1000, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 1250, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 1500, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 1500, Instrument: "Snare", Velocity: 1.0},
		{TimeMs: 1500, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 1750, Instrument: "HiHat", Velocity: 1.0},
	}

	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d:\n%+v", len(events), len(want), events)
	}
	// These timestamps are all exact in binary float64, so exact equality holds.
	if !reflect.DeepEqual(events, want) {
		t.Errorf("event slice mismatch\n got: %+v\nwant: %+v", events, want)
	}
}

func TestScheduleTiming(t *testing.T) {
	tests := []struct {
		name      string
		bpm       int
		sig       bt.Signature
		stepCount int
		hitIndex  int
		wantMs    float64
	}{
		{"4/4 120bpm 16 steps, index 4", 120, bt.Signature{Numerator: 4, Denominator: 4}, 16, 4, 500},
		{"4/4 120bpm 8 steps, index 2", 120, bt.Signature{Numerator: 4, Denominator: 4}, 8, 2, 500},
		{"4/4 120bpm 16 steps, index 0", 120, bt.Signature{Numerator: 4, Denominator: 4}, 16, 0, 0},
		{"6/8 120bpm 6 steps, index 1", 120, bt.Signature{Numerator: 6, Denominator: 8}, 6, 1, 500},
		{"6/8 120bpm 6 steps, last", 120, bt.Signature{Numerator: 6, Denominator: 8}, 6, 5, 2500},
		{"3-step non-integer, index 1", 120, bt.Signature{Numerator: 4, Denominator: 4}, 3, 1, 2000.0 / 3.0},
		{"3-step non-integer, index 2", 120, bt.Signature{Numerator: 4, Denominator: 4}, 3, 2, 4000.0 / 3.0},
		{"60bpm 4/4 4 steps, index 1", 60, bt.Signature{Numerator: 4, Denominator: 4}, 4, 1, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([]bt.Step, tt.stepCount)
			for i := range steps {
				if i == tt.hitIndex {
					steps[i] = hit("1")
				} else {
					steps[i] = rest()
				}
			}
			comp := &bt.Composition{
				BPM:       tt.bpm,
				Signature: tt.sig,
				Tracks:    []bt.Track{{Instrument: "X", Steps: steps, Pos: bt.Position{Line: 1, Column: 1}}},
			}
			events, err := Schedule("t.bt", comp, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1", len(events))
			}
			if math.Abs(events[0].TimeMs-tt.wantMs) > epsilon {
				t.Errorf("TimeMs = %v, want %v", events[0].TimeMs, tt.wantMs)
			}
		})
	}
}

func TestScheduleVelocity(t *testing.T) {
	// A stub resolver mirroring Spec 002's example velocity map.
	vel := func(instrument, symbol string) (float64, bool) {
		switch symbol {
		case "A":
			return 1.0, true
		case "a":
			return 0.5, true
		default:
			return 0, false
		}
	}

	tests := []struct {
		name     string
		symbol   string
		vel      VelocityFunc
		wantGain float64
		wantOK   bool
	}{
		{"numeric 1", "1", vel, 1.0, true},
		{"numeric 10", "10", vel, 1.0, true},
		{"numeric hit with nil resolver", "4", nil, 1.0, true},
		{"letter A", "A", vel, 1.0, true},
		{"letter a", "a", vel, 0.5, true},
		{"unknown letter", "Z", vel, 0, false},
		{"letter with nil resolver", "A", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &bt.Composition{
				BPM:       120,
				Signature: bt.Signature{Numerator: 4, Denominator: 4},
				Tracks:    []bt.Track{{Instrument: "Kick", Steps: []bt.Step{hit(tt.symbol)}, Pos: bt.Position{Line: 1, Column: 1}}},
			}
			events, err := Schedule("t.bt", comp, tt.vel)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(events) != 1 {
					t.Fatalf("got %d events, want 1", len(events))
				}
				if events[0].Velocity != tt.wantGain {
					t.Errorf("Velocity = %v, want %v", events[0].Velocity, tt.wantGain)
				}
				return
			}
			// Expect an error and a skipped event.
			if err == nil {
				t.Fatalf("expected an error, got nil (events: %+v)", events)
			}
			var list bt.ErrorList
			if !errors.As(err, &list) {
				t.Fatalf("error is not a bt.ErrorList: %T", err)
			}
			if len(events) != 0 {
				t.Errorf("got %d events, want 0 (event should be skipped)", len(events))
			}
		})
	}
}

// TestScheduleVelocityErrorPosition proves the step Pos propagates into the
// diagnostic (the headline file:line:col feature carried over from Spec 003).
func TestScheduleVelocityErrorPosition(t *testing.T) {
	comp := &bt.Composition{
		BPM:       120,
		Signature: bt.Signature{Numerator: 4, Denominator: 4},
		Tracks: []bt.Track{{
			Instrument: "Kick",
			Steps:      []bt.Step{rest(), hitAt("A", 4, 12)},
			Pos:        bt.Position{Line: 3, Column: 1},
		}},
	}
	_, err := Schedule("drum.bt", comp, nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var list bt.ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("error is not a bt.ErrorList: %T", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(list))
	}
	got := list[0].Error()
	if want := "drum.bt:4:12: "; !contains(got, want) {
		t.Errorf("diagnostic %q does not contain %q", got, want)
	}
	if !contains(got, "no velocity map available") {
		t.Errorf("diagnostic %q missing discriminating substring", got)
	}
}

func TestScheduleMultipleVelocityErrors(t *testing.T) {
	comp := &bt.Composition{
		BPM:       120,
		Signature: bt.Signature{Numerator: 4, Denominator: 4},
		Tracks: []bt.Track{{
			Instrument: "Kick",
			Steps:      []bt.Step{hitAt("A", 2, 1), hitAt("B", 2, 3)},
			Pos:        bt.Position{Line: 1, Column: 1},
		}},
	}
	_, err := Schedule("drum.bt", comp, func(_, _ string) (float64, bool) { return 0, false })
	var list bt.ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("error is not a bt.ErrorList: %T", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d diagnostics, want 2", len(list))
	}
	if list[0].Pos == list[1].Pos {
		t.Errorf("expected distinct positions, both were %v", list[0].Pos)
	}
}

func TestScheduleOrdering(t *testing.T) {
	// Two tracks with simultaneous hits at t=0 and t=1000; a rest contributes
	// nothing. Kick precedes HiHat at equal times by source (track) order.
	comp := &bt.Composition{
		BPM:       120,
		Signature: bt.Signature{Numerator: 4, Denominator: 4},
		Tracks: []bt.Track{
			grid("Kick", "1..."),  // hits at index 0 -> 0ms, index... only 0
			grid("HiHat", "1.1."), // hits at 0 -> 0ms, 2 -> 1000ms
		},
	}
	events, err := Schedule("t.bt", comp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []AudioEvent{
		{TimeMs: 0, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 0, Instrument: "HiHat", Velocity: 1.0},
		{TimeMs: 1000, Instrument: "HiHat", Velocity: 1.0},
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("ordering mismatch\n got: %+v\nwant: %+v", events, want)
	}
}

func TestScheduleAllRestTrack(t *testing.T) {
	comp := &bt.Composition{
		BPM:       120,
		Signature: bt.Signature{Numerator: 4, Denominator: 4},
		Tracks:    []bt.Track{grid("Kick", "....")},
	}
	events, err := Schedule("t.bt", comp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
	if events == nil {
		t.Error("events slice should be non-nil (len 0), got nil")
	}
}

func TestScheduleEmptyComposition(t *testing.T) {
	comp := &bt.Composition{BPM: 120, Signature: bt.Signature{Numerator: 4, Denominator: 4}}
	events, err := Schedule("t.bt", comp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if events == nil || len(events) != 0 {
		t.Errorf("want non-nil len-0 slice, got %#v", events)
	}
}

func TestScheduleDeterminism(t *testing.T) {
	a, err1 := Schedule("drum.bt", canonicalDrumBt(), nil)
	b, err2 := Schedule("drum.bt", canonicalDrumBt(), nil)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v / %v", err1, err2)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("non-deterministic output:\n a: %+v\n b: %+v", a, b)
	}
}

func TestScheduleGuards(t *testing.T) {
	tests := []struct {
		name string
		comp *bt.Composition
		want string // substring expected in the first diagnostic
	}{
		{"nil composition", nil, "nil composition"},
		{"zero bpm", &bt.Composition{BPM: 0, Signature: bt.Signature{Numerator: 4, Denominator: 4}}, "bpm must be positive"},
		{"negative bpm", &bt.Composition{BPM: -1, Signature: bt.Signature{Numerator: 4, Denominator: 4}}, "bpm must be positive"},
		{"zero numerator", &bt.Composition{BPM: 120, Signature: bt.Signature{Numerator: 0, Denominator: 4}}, "numerator must be positive"},
		{"zero denominator", &bt.Composition{BPM: 120, Signature: bt.Signature{Numerator: 4, Denominator: 0}}, "denominator must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := Schedule("t.bt", tt.comp, nil)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			var list bt.ErrorList
			if !errors.As(err, &list) {
				t.Fatalf("error is not a bt.ErrorList: %T", err)
			}
			if events != nil {
				t.Errorf("expected nil events on guard failure, got %+v", events)
			}
			if !contains(list[0].Error(), tt.want) {
				t.Errorf("diagnostic %q does not contain %q", list[0].Error(), tt.want)
			}
			if !contains(list[0].Error(), "1:1") {
				t.Errorf("guard diagnostic %q not anchored at 1:1", list[0].Error())
			}
		})
	}
}

func TestScheduleZeroStepTrack(t *testing.T) {
	comp := &bt.Composition{
		BPM:       120,
		Signature: bt.Signature{Numerator: 4, Denominator: 4},
		Tracks: []bt.Track{
			{Instrument: "Empty", Steps: nil, Pos: bt.Position{Line: 5, Column: 1}},
			grid("Kick", "1..."),
		},
	}
	events, err := Schedule("t.bt", comp, nil)
	if err == nil {
		t.Fatal("expected an error for the zero-step track")
	}
	var list bt.ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("error is not a bt.ErrorList: %T", err)
	}
	if !contains(list[0].Error(), "t.bt:5:1") {
		t.Errorf("diagnostic %q not anchored at the track Pos 5:1", list[0].Error())
	}
	// The valid track is still scheduled (collect-all recovery).
	if len(events) != 1 || events[0].Instrument != "Kick" {
		t.Errorf("expected the Kick track to still schedule, got %+v", events)
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1", true}, {"10", true}, {"0", true}, {"999", true},
		{"", false}, {"A", false}, {"a", false}, {"1a", false}, {".", false},
	}
	for _, tt := range tests {
		if got := isNumeric(tt.in); got != tt.want {
			t.Errorf("isNumeric(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// contains is a tiny substring helper to keep assertions readable without
// pulling in strings for one call site per test.
func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
