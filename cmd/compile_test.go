package cmd

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bryanbill/mac/internal/bt"
	goaudio "github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

func TestValidateBeatsDir(t *testing.T) {
	existingDir := t.TempDir()

	fileInsteadOfDir := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(fileInsteadOfDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
	}{
		{
			name:    "existing directory",
			input:   existingDir,
			wantErr: false,
		},
		{
			name:        "non-existent directory",
			input:       filepath.Join(existingDir, "nope"),
			wantErr:     true,
			errContains: "does not exist",
		},
		{
			name:        "path is a file",
			input:       fileInsteadOfDir,
			wantErr:     true,
			errContains: "not a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateBeatsDir(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateBeatsDir() = nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateBeatsDir() error = %v, want nil", err)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("validateBeatsDir() = %q, want absolute path", got)
			}
		})
	}
}

// writeWAV synthesizes a genuine 16-bit mono 44100 Hz WAV at path using the
// go-audio encoder. The engine needs decodable WAVs (not text placeholders).
func writeWAV(t *testing.T, path string, data []int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	enc := wav.NewEncoder(f, 44100, 16, 1, 1)
	buf := &goaudio.IntBuffer{
		Format:         &goaudio.Format{NumChannels: 1, SampleRate: 44100},
		Data:           data,
		SourceBitDepth: 16,
	}
	if err := enc.Write(buf); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// writeBT writes content to a .bt file named filename inside dir and returns
// the file's path.
func writeBT(t *testing.T, dir, filename, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestFindBeatFiles(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		wantCount   int
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty directory",
			setup:       func(t *testing.T, dir string) {},
			wantErr:     true,
			errContains: "no .bt files",
		},
		{
			name: "one bt file",
			setup: func(t *testing.T, dir string) {
				writeBT(t, dir, "a.bt", "bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . .\n")
			},
			wantCount: 1,
		},
		{
			name: "three bt files + one non-bt",
			setup: func(t *testing.T, dir string) {
				writeBT(t, dir, "c.bt", "bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . .\n")
				writeBT(t, dir, "a.bt", "bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . .\n")
				writeBT(t, dir, "b.bt", "bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . .\n")
				_ = os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("skip"), 0o644)
			},
			wantCount: 3,
		},
		{
			name: "nested bt in subdir",
			setup: func(t *testing.T, dir string) {
				writeBT(t, filepath.Join(dir, "sub"), "deep.bt",
					"bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . .\n")
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			files, err := findBeatFiles(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("findBeatFiles() = nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("findBeatFiles() error = %v", err)
			}
			if len(files) != tt.wantCount {
				t.Fatalf("got %d files, want %d", len(files), tt.wantCount)
			}
			// Assert sorted: each path <= the next.
			for i := 1; i < len(files); i++ {
				if files[i] < files[i-1] {
					t.Errorf("files not sorted: %q before %q", files[i-1], files[i])
				}
			}
		})
	}
}

func TestParseAndSchedule(t *testing.T) {
	dir := t.TempDir()
	path := writeBT(t, dir, "demo.bt",
		"bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . . 2 . . . 3 . . . 4 . . .\n")

	events, barDur, err := parseAndSchedule(path, nil)
	if err != nil {
		t.Fatalf("parseAndSchedule: %v", err)
	}

	// 4 numeric hits in 16 steps → 4 events.
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4", len(events))
	}

	// 4/4 @ 120 BPM → 2000 ms per bar.
	assertFloat(t, barDur, 2000.0, "bar duration")

	// All events should be within the bar [0, 2000).
	for i, ev := range events {
		if ev.TimeMs < 0 || ev.TimeMs >= 2000 {
			t.Errorf("event %d TimeMs = %v, want in [0, 2000)", i, ev.TimeMs)
		}
	}

	// First hit at step 0 → t=0; second at step 4 → t=500.
	if events[0].TimeMs != 0 {
		t.Errorf("first event TimeMs = %v, want 0", events[0].TimeMs)
	}
	if events[1].TimeMs != 500 {
		t.Errorf("second event TimeMs = %v, want 500", events[1].TimeMs)
	}
}

func TestParseAndSchedule_Error(t *testing.T) {
	dir := t.TempDir()
	path := writeBT(t, dir, "bad.bt", "bpm: 0\nsignature: 4/4\n\n[Kick]\n1 . . .\n")

	_, _, err := parseAndSchedule(path, nil)
	if err == nil {
		t.Fatal("expected error for invalid bpm")
	}
	// Schedule produces a positioned error (bt.ErrorList renders "file:line:col: msg").
	if !strings.Contains(err.Error(), "bad.bt") {
		t.Errorf("error %q does not name the file", err.Error())
	}
}

func TestVelocityFunc(t *testing.T) {
	maps := map[string]map[string]float64{
		"Kick": {"A": 1.0, "a": 0.5},
	}
	vel := velocityFunc(maps)

	tests := []struct {
		name       string
		instrument string
		symbol     string
		wantGain   float64
		wantOK     bool
	}{
		{"known uppercase", "Kick", "A", 1.0, true},
		{"known lowercase", "Kick", "a", 0.5, true},
		{"unknown symbol", "Kick", "Z", 0, false},
		{"unknown instrument", "Snare", "A", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gain, ok := vel(tt.instrument, tt.symbol)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && gain != tt.wantGain {
				t.Errorf("gain = %v, want %v", gain, tt.wantGain)
			}
		})
	}
}

func TestVelocityFunc_Nil(t *testing.T) {
	vel := velocityFunc(nil)
	_, ok := vel("Kick", "A")
	if ok {
		t.Fatal("expected ok=false for nil maps")
	}
}

func TestBarMsV2(t *testing.T) {
	tests := []struct {
		name string
		bpm  int
		num  int
		den  int
		want float64
	}{
		{"4/4 @ 120", 120, 4, 4, 2000},
		{"7/8 @ 140", 140, 7, 8, 3000.0},
		{"3/4 @ 60", 60, 3, 4, 3000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &bt.Composition{BPM: tt.bpm, Signature: bt.Signature{Numerator: tt.num, Denominator: tt.den}}
			got := barMsV2(comp)
			assertFloat(t, got, tt.want, "barMsV2")
		})
	}
}

func TestCompileCommand_HappyPath(t *testing.T) {
	beatsDir := t.TempDir()
	// A resting-only composition: zero events, but a valid .bt file. This keeps
	// the test hermetic regardless of the instrument library (no wav lookup).
	writeBT(t, beatsDir, "rests.bt",
		"bpm: 120\nsignature: 4/4\n\n[Kick]\n. . . . . . . . . . . . . . . .\n")

	dbFile := filepath.Join(t.TempDir(), "mac.db")
	missingInstruments := filepath.Join(t.TempDir(), "no-instruments")

	outFile := filepath.Join(t.TempDir(), "song.mp3")

	var out strings.Builder
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{
		"compile", beatsDir,
		"--db", dbFile,
		"--instruments", missingInstruments,
		"-o", outFile,
	})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("compile command error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "registered instruments: 0") {
		t.Fatalf("output missing instrument count:\n%s", got)
	}
	if !strings.Contains(got, "rendered") {
		t.Fatalf("output missing render confirmation:\n%s", got)
	}
	if _, err := os.Stat(dbFile); err != nil {
		t.Fatalf("database file not created: %v", err)
	}
	// A resting-only composition produces zero events, which the shine encoder
	// renders as a zero-byte file (valid, just silent). Assert the file is
	// created, not that it is non-empty — the non-empty assertion lives in
	// TestCompile_RendersRealMP3, which uses real hits.
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("output mp3 not created: %v", err)
	}
}

func TestCompileCommand_EmptyBeatsErrors(t *testing.T) {
	beatsDir := t.TempDir()
	dbFile := filepath.Join(t.TempDir(), "mac.db")
	missingInstruments := filepath.Join(t.TempDir(), "no-instruments")

	var out strings.Builder
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{
		"compile", beatsDir,
		"--db", dbFile,
		"--instruments", missingInstruments,
		"-o", filepath.Join(t.TempDir(), "never.mp3"),
	})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for empty beats directory, got nil")
	}
	if !strings.Contains(err.Error(), "no .bt files") {
		t.Fatalf("error %q does not mention empty beats", err.Error())
	}
}

func TestCompileCommand_RegistersInstruments(t *testing.T) {
	beatsDir := t.TempDir()
	// Resting-only composition so the placeholder kick.wav is never loaded.
	writeBT(t, beatsDir, "rests.bt",
		"bpm: 120\nsignature: 4/4\n\n[Kick]\n. . . . . . . . . . . . . . . .\n")

	dbFile := filepath.Join(t.TempDir(), "mac.db")

	// A minimal one-instrument library.
	instrumentsDir := t.TempDir()
	kickDir := filepath.Join(instrumentsDir, "kick")
	if err := os.MkdirAll(kickDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kickDir, "manifest.json"),
		[]byte(`{"id":"Kick","name":"Kick","sample":"kick.wav"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kickDir, "kick.wav"), []byte("wav"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	var out strings.Builder
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{
		"compile", beatsDir,
		"--db", dbFile,
		"--instruments", instrumentsDir,
		"-o", filepath.Join(t.TempDir(), "song.mp3"),
	})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("compile command error = %v", err)
	}

	if got := out.String(); !strings.Contains(got, "registered instruments: 1") {
		t.Fatalf("output missing registered instrument:\n%s", got)
	}
}

// TestCompile_RendersRealMP3 is the headline integration test: it builds a
// real beats dir with a .bt that hits an instrument, a real instrument library
// with a genuine WAV, runs the full compile pipeline through cobra, and
// asserts a non-empty, frame-sync-valid, byte-identical MP3 is produced.
func TestCompile_RendersRealMP3(t *testing.T) {
	beatsDir := t.TempDir()
	writeBT(t, beatsDir, "demo.bt",
		"bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . . 2 . . . 3 . . . 4 . . .\n")

	instrumentsDir := t.TempDir()
	kickDir := filepath.Join(instrumentsDir, "kick")
	if err := os.MkdirAll(kickDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kickDir, "manifest.json"),
		[]byte(`{"id":"Kick","name":"Kick","sample":"kick.wav"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeWAV(t, filepath.Join(kickDir, "kick.wav"),
		[]int{16000, -16000, 8000, -8000, 4000, -4000, 2000, -2000})

	dbFile := filepath.Join(t.TempDir(), "mac.db")
	outDir := t.TempDir()
	out1 := filepath.Join(outDir, "a.mp3")
	out2 := filepath.Join(outDir, "b.mp3")

	for i := 0; i < 2; i++ {
		out := filepath.Join(outDir, []string{"a.mp3", "b.mp3"}[i])
		var buf strings.Builder
		rootCmd.SetOut(&buf)
		rootCmd.SetErr(&buf)
		rootCmd.SetArgs([]string{
			"compile", beatsDir,
			"--db", dbFile,
			"--instruments", instrumentsDir,
			"-o", out,
		})
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("compile run %d error = %v", i+1, err)
		}
	}
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	b1, err := os.ReadFile(out1)
	if err != nil {
		t.Fatalf("read %s: %v", out1, err)
	}
	b2, err := os.ReadFile(out2)
	if err != nil {
		t.Fatalf("read %s: %v", out2, err)
	}

	// Non-empty.
	if len(b1) == 0 {
		t.Fatal("output.mp3 is empty")
	}

	// Frame sync valid.
	assertFrameSync(t, b1)

	// Deterministic: two runs with identical inputs produce byte-identical output.
	if !bytes.Equal(b1, b2) {
		t.Errorf("renders differ: %d vs %d bytes (not byte-identical)", len(b1), len(b2))
	}
}

func TestCompile_UnknownInstrumentErrors(t *testing.T) {
	beatsDir := t.TempDir()
	writeBT(t, beatsDir, "ghost.bt",
		"bpm: 120\nsignature: 4/4\n\n[Ghost]\n1 . . .\n")

	// An instrument library with a Kick (but no Ghost).
	instrumentsDir := t.TempDir()
	kickDir := filepath.Join(instrumentsDir, "kick")
	if err := os.MkdirAll(kickDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kickDir, "manifest.json"),
		[]byte(`{"id":"Kick","name":"Kick","sample":"kick.wav"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeWAV(t, filepath.Join(kickDir, "kick.wav"), []int{100, -100})

	dbFile := filepath.Join(t.TempDir(), "mac.db")

	var buf strings.Builder
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"compile", beatsDir,
		"--db", dbFile,
		"--instruments", instrumentsDir,
		"-o", filepath.Join(t.TempDir(), "out.mp3"),
	})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown instrument, got nil")
	}
	if !strings.Contains(err.Error(), "Ghost") {
		t.Fatalf("error %q does not mention unknown instrument Ghost", err.Error())
	}
}

func TestCompile_MultiFileConcatenation(t *testing.T) {
	beatsDir := t.TempDir()
	// Two `.bt` files; filenames sort section-a before section-b.
	writeBT(t, beatsDir, "01-a.bt",
		"bpm: 120\nsignature: 4/4\n\n[Kick]\n1 . . .\n")
	writeBT(t, beatsDir, "02-b.bt",
		"bpm: 120\nsignature: 4/4\n\n[Kick]\n2 . . .\n")

	instrumentsDir := t.TempDir()
	kickDir := filepath.Join(instrumentsDir, "kick")
	if err := os.MkdirAll(kickDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kickDir, "manifest.json"),
		[]byte(`{"id":"Kick","name":"Kick","sample":"kick.wav"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeWAV(t, filepath.Join(kickDir, "kick.wav"), []int{16000, -16000, 8000, -8000})

	dbFile := filepath.Join(t.TempDir(), "mac.db")
	out := filepath.Join(t.TempDir(), "out.mp3")

	var buf strings.Builder
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{
		"compile", beatsDir,
		"--db", dbFile,
		"--instruments", instrumentsDir,
		"-o", out,
	})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("compile error = %v", err)
	}

	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Fatalf("output not created or empty: %v", err)
	}
	if !strings.Contains(buf.String(), "rendered") {
		t.Fatalf("output missing render confirmation:\n%s", buf.String())
	}
}

// assertFloat compares two floats with a tight tolerance.
func assertFloat(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// assertFrameSync checks the buffer begins with an MPEG audio frame sync word
// (11 set bits): b[0]==0xFF and the top three bits of b[1] set.
func assertFrameSync(t *testing.T, b []byte) {
	t.Helper()
	if len(b) < 2 {
		t.Fatalf("output too short (%d bytes) for frame sync", len(b))
	}
	if b[0] != 0xFF || (b[1]&0xE0) != 0xE0 {
		t.Errorf("no MPEG frame sync: b[0]=0x%02X b[1]=0x%02X", b[0], b[1])
	}
}
