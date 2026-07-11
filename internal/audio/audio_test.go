package audio

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/bryanbill/mac/internal/schedule"
	goaudio "github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// writeWAV synthesizes a genuine WAV file at path using the go-audio encoder.
// data is interleaved integer PCM in the source bit depth's signed range. The
// committed .wav fixtures elsewhere in the tree are text placeholders and are
// intentionally not reused: the engine needs decodable WAVs.
func writeWAV(t *testing.T, path string, data []int, sampleRate, bitDepth, channels int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	enc := wav.NewEncoder(f, sampleRate, bitDepth, channels, 1)
	buf := &goaudio.IntBuffer{
		Format:         &goaudio.Format{NumChannels: channels, SampleRate: sampleRate},
		Data:           data,
		SourceBitDepth: bitDepth,
	}
	if err := enc.Write(buf); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

const eps = 1e-4

func TestLoadWAV_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		data     []int
		rate     int
		channels int
	}{
		{"mono", []int{0, 16384, -16384, 32767}, 44100, 1},
		{"stereo", []int{0, 100, -100, 200, 16384, -16384}, 48000, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.wav")
			writeWAV(t, path, tt.data, tt.rate, 16, tt.channels)

			s, err := LoadWAV(path)
			if err != nil {
				t.Fatalf("LoadWAV: %v", err)
			}
			if s.SampleRate != tt.rate {
				t.Errorf("SampleRate = %d, want %d", s.SampleRate, tt.rate)
			}
			if s.NumChannels != tt.channels {
				t.Errorf("NumChannels = %d, want %d", s.NumChannels, tt.channels)
			}
			if len(s.Data) != len(tt.data) {
				t.Fatalf("len(Data) = %d, want %d", len(s.Data), len(tt.data))
			}
			for i, raw := range tt.data {
				want := float64(raw) / 32768.0
				if math.Abs(s.Data[i]-want) > eps {
					t.Errorf("Data[%d] = %v, want ~%v", i, s.Data[i], want)
				}
			}
			if got := s.Frames(); got != len(tt.data)/tt.channels {
				t.Errorf("Frames() = %d, want %d", got, len(tt.data)/tt.channels)
			}
		})
	}
}

func TestLoadWAV_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := LoadWAV(filepath.Join(t.TempDir(), "nope.wav")); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("text placeholder", func(t *testing.T) {
		// The committed scan fixture is a text placeholder, not real WAV bytes:
		// proves LoadWAV actually decodes rather than byte-copying.
		path := "../instruments/testdata/library/kick/kick.wav"
		if _, err := LoadWAV(path); err == nil {
			t.Fatal("expected error decoding text-placeholder wav")
		}
	})

	t.Run("8-bit unsupported", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "eight.wav")
		writeWAV(t, path, []int{0, 64, -64, 100}, 44100, 8, 1)
		_, err := LoadWAV(path)
		if err == nil {
			t.Fatal("expected error for 8-bit WAV")
		}
	})
}

// mono builds a single-channel Sample at 44100 Hz from raw float values.
func mono(data ...float64) *Sample {
	return &Sample{Data: data, SampleRate: 44100, NumChannels: 1}
}

func TestMix_Placement(t *testing.T) {
	tests := []struct {
		name      string
		timeMs    float64
		wantFrame int
	}{
		{"t=0", 0, 0},
		{"t=500ms@44100", 500, 22050},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := map[string]*Sample{"Kick": mono(0.25, 0.5)}
			events := []schedule.AudioEvent{{TimeMs: tt.timeMs, Instrument: "Kick", Velocity: 1.0}}

			mix, err := Mix(events, samples)
			if err != nil {
				t.Fatalf("Mix: %v", err)
			}
			base := tt.wantFrame // mono → base == frame
			for i := 0; i < base; i++ {
				if mix.Data[i] != 0 {
					t.Fatalf("Data[%d] = %v, want 0 (leading silence)", i, mix.Data[i])
				}
			}
			if mix.Data[base] != 0.25 || mix.Data[base+1] != 0.5 {
				t.Errorf("placed = [%v %v], want [0.25 0.5]", mix.Data[base], mix.Data[base+1])
			}
		})
	}
}

func TestMix_RoundingHalfFrame(t *testing.T) {
	// TimeMs chosen so TimeMs/1000*rate = 22050.5 → math.Round → 22051.
	timeMs := 22050.5 / 44100.0 * 1000.0
	samples := map[string]*Sample{"Kick": mono(1.0)}
	events := []schedule.AudioEvent{{TimeMs: timeMs, Instrument: "Kick", Velocity: 1.0}}
	mix, err := Mix(events, samples)
	if err != nil {
		t.Fatalf("Mix: %v", err)
	}
	if mix.Data[22051] != 1.0 {
		t.Errorf("expected hit at frame 22051 (round of 22050.5), got Data[22051]=%v", mix.Data[22051])
	}
}

func TestMix_Velocity(t *testing.T) {
	for _, vel := range []float64{1.0, 0.5, 0.0} {
		samples := map[string]*Sample{"Kick": mono(1.0, -1.0)}
		events := []schedule.AudioEvent{{TimeMs: 0, Instrument: "Kick", Velocity: vel}}
		mix, err := Mix(events, samples)
		if err != nil {
			t.Fatalf("Mix vel=%v: %v", vel, err)
		}
		if mix.Data[0] != vel || mix.Data[1] != -vel {
			t.Errorf("vel=%v: got [%v %v], want [%v %v]", vel, mix.Data[0], mix.Data[1], vel, -vel)
		}
	}
}

func TestMix_Overlap(t *testing.T) {
	// Two events one frame apart; sample length 3 → overlap at frames 1,2.
	samples := map[string]*Sample{"Kick": mono(0.5, 0.5, 0.5)}
	frameMs := 1.0 / 44100.0 * 1000.0
	events := []schedule.AudioEvent{
		{TimeMs: 0, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: frameMs, Instrument: "Kick", Velocity: 1.0},
	}
	mix, err := Mix(events, samples)
	if err != nil {
		t.Fatalf("Mix: %v", err)
	}
	// frame0: 0.5; frame1: 0.5+0.5=1.0; frame2: 0.5+0.5=1.0; frame3: 0.5
	want := []float64{0.5, 1.0, 1.0, 0.5}
	if len(mix.Data) != len(want) {
		t.Fatalf("len = %d, want %d", len(mix.Data), len(want))
	}
	for i, w := range want {
		if math.Abs(mix.Data[i]-w) > eps {
			t.Errorf("Data[%d] = %v, want %v", i, mix.Data[i], w)
		}
	}
}

func TestMix_Stereo(t *testing.T) {
	// 2-channel sample, one frame per L/R pair.
	samples := map[string]*Sample{
		"Kick": {Data: []float64{0.1, 0.2, 0.3, 0.4}, SampleRate: 44100, NumChannels: 2},
	}
	// place at frame 1 → base = 1*2 = 2.
	frameMs := 1.0 / 44100.0 * 1000.0
	events := []schedule.AudioEvent{{TimeMs: frameMs, Instrument: "Kick", Velocity: 0.5}}
	mix, err := Mix(events, samples)
	if err != nil {
		t.Fatalf("Mix: %v", err)
	}
	if mix.NumChannels != 2 {
		t.Fatalf("NumChannels = %d, want 2", mix.NumChannels)
	}
	want := []float64{0, 0, 0.05, 0.1, 0.15, 0.2}
	if len(mix.Data) != len(want) {
		t.Fatalf("len = %d, want %d", len(mix.Data), len(want))
	}
	for i, w := range want {
		if math.Abs(mix.Data[i]-w) > eps {
			t.Errorf("Data[%d] = %v, want %v", i, mix.Data[i], w)
		}
	}
}

func TestMix_MissingInstrument(t *testing.T) {
	samples := map[string]*Sample{"Kick": mono(1.0)}
	events := []schedule.AudioEvent{
		{TimeMs: 0, Instrument: "Tom", Velocity: 1.0},
		{TimeMs: 0, Instrument: "Clap", Velocity: 1.0},
	}
	_, err := Mix(events, samples)
	if err == nil {
		t.Fatal("expected missing-instrument error")
	}
	msg := err.Error()
	if !contains(msg, `"Clap"`) || !contains(msg, `"Tom"`) {
		t.Errorf("error should name missing instruments, got %q", msg)
	}
	// sorted: Clap before Tom
	if idxOf(msg, `"Clap"`) > idxOf(msg, `"Tom"`) {
		t.Errorf("missing instruments not sorted: %q", msg)
	}
}

func TestMix_FormatMismatch(t *testing.T) {
	t.Run("sample rate", func(t *testing.T) {
		samples := map[string]*Sample{
			"A": {Data: []float64{1}, SampleRate: 44100, NumChannels: 1},
			"B": {Data: []float64{1}, SampleRate: 48000, NumChannels: 1},
		}
		if _, err := Mix(nil, samples); err == nil {
			t.Fatal("expected sample-rate mismatch error")
		}
	})
	t.Run("channels", func(t *testing.T) {
		samples := map[string]*Sample{
			"A": {Data: []float64{1}, SampleRate: 44100, NumChannels: 1},
			"B": {Data: []float64{1, 1}, SampleRate: 44100, NumChannels: 2},
		}
		if _, err := Mix(nil, samples); err == nil {
			t.Fatal("expected channel mismatch error")
		}
	})
}

func TestMix_Empty(t *testing.T) {
	mix, err := Mix(nil, map[string]*Sample{})
	if err != nil {
		t.Fatalf("Mix: %v", err)
	}
	if len(mix.Data) != 0 {
		t.Errorf("len(Data) = %d, want 0", len(mix.Data))
	}
	if mix.SampleRate != defaultSampleRate || mix.NumChannels != defaultChannels {
		t.Errorf("format = %d/%d, want %d/%d", mix.SampleRate, mix.NumChannels, defaultSampleRate, defaultChannels)
	}
}

func TestFloatToInt16(t *testing.T) {
	tests := []struct {
		in   float64
		want int16
	}{
		{0, 0},
		{1.0, 32767},   // clip
		{-1.0, -32768}, // clip
		{2.0, 32767},   // over → clip
		{-2.0, -32768}, // under → clip
		{0.5, 16384},
		{-0.5, -16384},
	}
	for _, tt := range tests {
		if got := floatToInt16(tt.in); got != tt.want {
			t.Errorf("floatToInt16(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestEncodeMP3_Valid(t *testing.T) {
	samples := map[string]*Sample{"Kick": mono(0.5, -0.5, 0.25, -0.25)}
	events := []schedule.AudioEvent{{TimeMs: 0, Instrument: "Kick", Velocity: 1.0}}
	mix, err := Mix(events, samples)
	if err != nil {
		t.Fatalf("Mix: %v", err)
	}
	var buf bytes.Buffer
	if err := EncodeMP3(&buf, mix); err != nil {
		t.Fatalf("EncodeMP3: %v", err)
	}
	b := buf.Bytes()
	if len(b) == 0 {
		t.Fatal("EncodeMP3 produced no output")
	}
	assertFrameSync(t, b)
}

func TestRender_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	kick := filepath.Join(dir, "kick.wav")
	snare := filepath.Join(dir, "snare.wav")
	writeWAV(t, kick, []int{16000, -16000, 8000, -8000}, 44100, 16, 1)
	writeWAV(t, snare, []int{4000, -4000}, 44100, 16, 1)

	events := []schedule.AudioEvent{
		{TimeMs: 0, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 500, Instrument: "Snare", Velocity: 0.5},
		{TimeMs: 1000, Instrument: "Kick", Velocity: 1.0},
	}
	paths := map[string]string{"Kick": kick, "Snare": snare}

	out := filepath.Join(dir, "output.mp3")
	if err := Render(events, paths, out); err != nil {
		t.Fatalf("Render: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("output.mp3 is empty")
	}
	assertFrameSync(t, b)
}

func TestRender_Deterministic(t *testing.T) {
	dir := t.TempDir()
	kick := filepath.Join(dir, "kick.wav")
	writeWAV(t, kick, []int{16000, -16000, 8000, -8000}, 44100, 16, 1)

	events := []schedule.AudioEvent{
		{TimeMs: 0, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 250, Instrument: "Kick", Velocity: 0.5},
	}
	paths := map[string]string{"Kick": kick}

	out1 := filepath.Join(dir, "a.mp3")
	out2 := filepath.Join(dir, "b.mp3")
	if err := Render(events, paths, out1); err != nil {
		t.Fatalf("Render 1: %v", err)
	}
	if err := Render(events, paths, out2); err != nil {
		t.Fatalf("Render 2: %v", err)
	}
	b1, _ := os.ReadFile(out1)
	b2, _ := os.ReadFile(out2)
	if !bytes.Equal(b1, b2) {
		t.Errorf("renders differ: %d vs %d bytes (not byte-identical)", len(b1), len(b2))
	}
}

func TestRender_MissingPath(t *testing.T) {
	dir := t.TempDir()
	kick := filepath.Join(dir, "kick.wav")
	writeWAV(t, kick, []int{100, -100}, 44100, 16, 1)

	events := []schedule.AudioEvent{
		{TimeMs: 0, Instrument: "Kick", Velocity: 1.0},
		{TimeMs: 0, Instrument: "Ghost", Velocity: 1.0},
	}
	paths := map[string]string{"Kick": kick}
	err := Render(events, paths, filepath.Join(dir, "out.mp3"))
	if err == nil {
		t.Fatal("expected missing-path error")
	}
	if !contains(err.Error(), `"Ghost"`) {
		t.Errorf("error should name missing instrument, got %q", err.Error())
	}
}

// assertFrameSync checks the buffer begins with an MPEG audio frame sync word
// (11 set bits): b[0]==0xFF and the top three bits of b[1] set. Structural
// validity without depending on a full MP3 decoder.
func assertFrameSync(t *testing.T, b []byte) {
	t.Helper()
	if len(b) < 2 {
		t.Fatalf("output too short (%d bytes) for frame sync", len(b))
	}
	if b[0] != 0xFF || (b[1]&0xE0) != 0xE0 {
		t.Errorf("no MPEG frame sync: b[0]=0x%02X b[1]=0x%02X", b[0], b[1])
	}
}

func contains(s, sub string) bool { return idxOf(s, sub) >= 0 }

func idxOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
