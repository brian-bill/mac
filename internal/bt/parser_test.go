package bt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_HappyPath(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "drum.bt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	comp, err := Parse("drum.bt", src)
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	if comp.BPM != 120 {
		t.Errorf("BPM = %d, want 120", comp.BPM)
	}
	if comp.Signature != (Signature{4, 4}) {
		t.Errorf("Signature = %s, want 4/4", comp.Signature)
	}
	if got := comp.Signature.String(); got != "4/4" {
		t.Errorf("Signature.String() = %q, want 4/4", got)
	}
	if len(comp.Tracks) != 3 {
		t.Fatalf("len(Tracks) = %d, want 3", len(comp.Tracks))
	}

	wantNames := []string{"Kick", "Snare", "HiHat"}
	for i, want := range wantNames {
		if comp.Tracks[i].Instrument != want {
			t.Errorf("Tracks[%d].Instrument = %q, want %q", i, comp.Tracks[i].Instrument, want)
		}
		if len(comp.Tracks[i].Steps) != 16 {
			t.Errorf("Tracks[%d] step count = %d, want 16", i, len(comp.Tracks[i].Steps))
		}
	}

	// Spot-check kinds/symbols on the Kick track: hit at index 0, rest at 1.
	kick := comp.Tracks[0]
	if kick.Steps[0].Kind != StepHit || kick.Steps[0].Symbol != "1" {
		t.Errorf("Kick[0] = %+v, want StepHit \"1\"", kick.Steps[0])
	}
	if kick.Steps[1].Kind != StepRest || kick.Steps[1].Symbol != "." {
		t.Errorf("Kick[1] = %+v, want StepRest \".\"", kick.Steps[1])
	}
	if kick.Steps[4].Symbol != "2" {
		t.Errorf("Kick[4].Symbol = %q, want \"2\"", kick.Steps[4].Symbol)
	}
	// Header position points at the '[' on its line.
	if kick.Pos != (Position{4, 1}) {
		t.Errorf("Kick.Pos = %s, want 4:1", kick.Pos)
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantSub string   // substring the (first) error must contain
		wantPos string   // "line:col" the error must carry
		more    []string // additional substrings expected somewhere in the list
	}{
		{
			name:    "missing bpm",
			src:     "signature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: "missing required metadata: bpm",
			wantPos: "1:1",
		},
		{
			name:    "missing signature",
			src:     "bpm: 120\n\n[Kick]\n1 .\n",
			wantSub: "missing required metadata: signature",
			wantPos: "1:1",
		},
		{
			name:    "bpm not an integer, column points at value",
			src:     "bpm:   fast\nsignature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: `expected integer for bpm, got IDENT "fast"`,
			wantPos: "1:8", // 'f' of fast, not the ':'
		},
		{
			name:    "bpm zero",
			src:     "bpm: 0\nsignature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: "bpm must be a positive integer",
			wantPos: "1:6",
		},
		{
			name:    "signature missing slash",
			src:     "bpm: 120\nsignature: 4\n\n[Kick]\n1 .\n",
			wantSub: "expected '/' in signature",
			wantPos: "2:13",
		},
		{
			name:    "signature numerator not an integer",
			src:     "bpm: 120\nsignature: x/4\n\n[Kick]\n1 .\n",
			wantSub: `expected integer for signature numerator, got IDENT "x"`,
			wantPos: "2:12",
		},
		{
			name:    "signature denominator not an integer",
			src:     "bpm: 120\nsignature: 4/x\n\n[Kick]\n1 .\n",
			wantSub: `expected integer for signature denominator, got IDENT "x"`,
			wantPos: "2:14",
		},
		{
			name:    "signature numerator zero",
			src:     "bpm: 120\nsignature: 0/4\n\n[Kick]\n1 .\n",
			wantSub: "signature numerator must be positive",
			wantPos: "2:12",
		},
		{
			name:    "bpm value overflows int",
			src:     "bpm: 99999999999999999999\nsignature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: "bpm value out of range",
			wantPos: "1:6",
		},
		{
			name:    "trailing token after bpm value",
			src:     "bpm: 120 4\nsignature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: "unexpected INT \"4\" after metadata value",
			wantPos: "1:10",
		},
		{
			name:    "junk after track header on same line",
			src:     "bpm: 120\nsignature: 4/4\n\n[Kick] x\n1 .\n",
			wantSub: "unexpected IDENT \"x\" after track header [Kick]",
			wantPos: "4:8",
		},
		{
			name:    "stray token where header expected",
			src:     "bpm: 120\nsignature: 4/4\n\n5\n",
			wantSub: `expected track header '[', got INT "5"`,
			wantPos: "4:1",
		},
		{
			name:    "signature denominator zero",
			src:     "bpm: 120\nsignature: 4/0\n\n[Kick]\n1 .\n",
			wantSub: "signature denominator must be positive",
			wantPos: "2:14",
		},
		{
			name:    "unknown metadata key",
			src:     "bmp: 120\nsignature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: `unknown metadata key "bmp"`,
			wantPos: "1:1",
		},
		{
			name:    "duplicate bpm",
			src:     "bpm: 120\nbpm: 130\nsignature: 4/4\n\n[Kick]\n1 .\n",
			wantSub: `duplicate metadata key "bpm"`,
			wantPos: "2:1",
		},
		{
			name:    "unclosed header",
			src:     "bpm: 120\nsignature: 4/4\n\n[Kick\n1 .\n",
			wantSub: "expected ']' to close track header",
			wantPos: "4:6", // where ']' was expected (end of "[Kick")
		},
		{
			name:    "empty header",
			src:     "bpm: 120\nsignature: 4/4\n\n[]\n1 .\n",
			wantSub: "expected instrument name after '['",
			wantPos: "4:2",
		},
		{
			name:    "header with no sequence at EOF",
			src:     "bpm: 120\nsignature: 4/4\n\n[Kick]\n",
			wantSub: "track [Kick] has no sequence",
			wantPos: "4:1",
		},
		{
			name:    "stray colon in sequence",
			src:     "bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . : .\n",
			wantSub: "unexpected COLON \":\" in sequence for [Kick]",
			wantPos: "5:5",
		},
		{
			name:    "hash comment is illegal",
			src:     "bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . # .\n",
			wantSub: `unexpected character "#" in sequence for [Kick]`,
			wantPos: "5:5",
		},
		{
			name:    "empty file reports both missing keys and no tracks",
			src:     "",
			wantSub: "missing required metadata: bpm",
			wantPos: "1:1",
			more:    []string{"missing required metadata: signature", "file has no tracks"},
		},
		{
			name:    "metadata only, no tracks",
			src:     "bpm: 120\nsignature: 4/4\n",
			wantSub: "file has no tracks",
			wantPos: "1:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("t.bt", []byte(tt.src))
			if err == nil {
				t.Fatalf("Parse() = nil error, want error")
			}

			var list ErrorList
			if !errors.As(err, &list) {
				t.Fatalf("error is not an ErrorList: %T", err)
			}

			// The full message must carry the file, position, and substring.
			full := err.Error()
			if !strings.Contains(full, tt.wantSub) {
				t.Errorf("error %q does not contain %q", full, tt.wantSub)
			}

			// Assert the expected position appears on some error in the list
			// (usually the first) alongside its message.
			foundPos := false
			for _, e := range list {
				if e.Pos.String() == tt.wantPos {
					foundPos = true
				}
			}
			if !foundPos {
				t.Errorf("no error at position %s; got list: %v", tt.wantPos, list)
			}

			// Every error must be prefixed with the diagnostic filename.
			for _, e := range list {
				if !strings.HasPrefix(e.Error(), "t.bt:") {
					t.Errorf("error %q not prefixed with filename", e.Error())
				}
			}

			for _, sub := range tt.more {
				if !strings.Contains(joinErrs(list), sub) {
					t.Errorf("error list %v missing %q", list, sub)
				}
			}
		})
	}
}

func TestParse_MultipleErrors(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "multi-error.bt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	_, perr := Parse("multi-error.bt", src)
	if perr == nil {
		t.Fatal("Parse() = nil error, want ErrorList")
	}
	var list ErrorList
	if !errors.As(perr, &list) {
		t.Fatalf("error is not an ErrorList: %T", perr)
	}
	if len(list) < 2 {
		t.Fatalf("want >= 2 errors from recovery, got %d: %v", len(list), list)
	}

	// Positions must be distinct — proof that recovery advanced past each.
	seen := map[string]bool{}
	for _, e := range list {
		seen[e.Pos.String()] = true
	}
	if len(seen) < 2 {
		t.Errorf("errors not at distinct positions: %v", list)
	}

	// ErrorList.Error() summarizes with "(and N more)".
	if !strings.Contains(list.Error(), "and ") {
		t.Errorf("ErrorList.Error() = %q, want an (and N more) summary", list.Error())
	}
}

func TestParse_VelocityLettersCarried(t *testing.T) {
	src := "bpm: 120\nsignature: 4/4\n\n[Kick]\nA . a . 1 .\n"
	comp, err := Parse("t.bt", []byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	steps := comp.Tracks[0].Steps
	// A and a are hits carrying their raw symbol; dots are rests.
	if steps[0].Kind != StepHit || steps[0].Symbol != "A" {
		t.Errorf("steps[0] = %+v, want StepHit \"A\"", steps[0])
	}
	if steps[2].Kind != StepHit || steps[2].Symbol != "a" {
		t.Errorf("steps[2] = %+v, want StepHit \"a\"", steps[2])
	}
	if steps[1].Kind != StepRest {
		t.Errorf("steps[1] = %+v, want StepRest", steps[1])
	}
}

func TestParse_LenientBlankLines(t *testing.T) {
	// Missing blank line between tracks, and extra blank lines: both tolerated.
	src := "bpm: 120\nsignature: 4/4\n[Kick]\n1 .\n\n\n\n[Snare]\n. 2\n"
	comp, err := Parse("t.bt", []byte(src))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil (lenient blank lines)", err)
	}
	if len(comp.Tracks) != 2 {
		t.Fatalf("len(Tracks) = %d, want 2", len(comp.Tracks))
	}
}

func TestParse_RecoversAndFindsLaterTrack(t *testing.T) {
	// A stray token where a header is expected must not swallow the next track.
	src := "bpm: 120\nsignature: 4/4\n\n?\n\n[Snare]\n. 2\n"
	comp, err := Parse("t.bt", []byte(src))
	if err == nil {
		t.Fatal("Parse() = nil error, want error for stray token")
	}
	if len(comp.Tracks) != 1 || comp.Tracks[0].Instrument != "Snare" {
		t.Fatalf("recovery failed to find Snare: %+v", comp.Tracks)
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beat.bt")
	content := "bpm: 90\nsignature: 3/4\n\n[Kick]\n1 . .\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	comp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	if comp.BPM != 90 || comp.Signature != (Signature{3, 4}) {
		t.Errorf("got BPM=%d Signature=%s, want 90 3/4", comp.BPM, comp.Signature)
	}
}

func TestParseFile_DiagnosticUsesBaseName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.bt")
	if err := os.WriteFile(path, []byte("bpm: nope\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("ParseFile() = nil error, want error")
	}
	// Diagnostics carry the base name, never the full temp path.
	if !strings.Contains(err.Error(), "broken.bt:") {
		t.Errorf("error %q missing base name", err.Error())
	}
	if strings.Contains(err.Error(), dir) {
		t.Errorf("error %q leaked full path", err.Error())
	}
}

func TestParseFile_ReadError(t *testing.T) {
	_, err := ParseFile(filepath.Join(t.TempDir(), "does-not-exist.bt"))
	if err == nil {
		t.Fatal("ParseFile() = nil error, want read error")
	}
	if !strings.Contains(err.Error(), "read .bt file") {
		t.Errorf("error %q missing read context", err.Error())
	}
}

func TestErrorListEdgeCases(t *testing.T) {
	if got := (ErrorList{}).Error(); got != "no errors" {
		t.Errorf("empty ErrorList.Error() = %q, want %q", got, "no errors")
	}
	single := ErrorList{{File: "f.bt", Pos: Position{1, 1}, Msg: "boom"}}
	if got := single.Error(); got != "f.bt:1:1: boom" {
		t.Errorf("single ErrorList.Error() = %q", got)
	}
	if got := StepHit.String(); got != "StepHit" {
		t.Errorf("StepHit.String() = %q", got)
	}
	if got := StepKind(9).String(); got != "StepKind(9)" {
		t.Errorf("unknown StepKind.String() = %q", got)
	}
}

// joinErrs concatenates every error's message for substring assertions.
func joinErrs(list ErrorList) string {
	var b strings.Builder
	for _, e := range list {
		b.WriteString(e.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
