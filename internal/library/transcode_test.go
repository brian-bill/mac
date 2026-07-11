package library

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// writeTestWAV writes a PCM WAV fixture with the given format and int samples
// (interleaved) and returns its path.
func writeTestWAV(t *testing.T, dir string, sampleRate, channels, bitDepth int, data []int) string {
	t.Helper()
	path := filepath.Join(dir, "in.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := wav.NewEncoder(f, sampleRate, bitDepth, channels, wavFormatPCM)
	ib := &audio.IntBuffer{
		Format:         &audio.Format{SampleRate: sampleRate, NumChannels: channels},
		Data:           data,
		SourceBitDepth: bitDepth,
	}
	if err := enc.Write(ib); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

// decodeWAVBytes decodes canonical WAV bytes into a Sample-like triple.
func decodeWAVBytes(t *testing.T, dir string, b []byte) (rate, ch, bits int, data []int) {
	t.Helper()
	p := filepath.Join(dir, "out.wav")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := wav.NewDecoder(f)
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Format.SampleRate, buf.Format.NumChannels, buf.SourceBitDepth, buf.Data
}

func TestDownmixMono(t *testing.T) {
	// Stereo L=1.0, R=0.0 → mono 0.5 for each frame.
	in := []float64{1.0, 0.0, 0.5, -0.5}
	got := downmixMono(in, 2)
	want := []float64{0.5, 0.0}
	if len(got) != len(want) {
		t.Fatalf("len %d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("frame %d = %v, want %v", i, got[i], want[i])
		}
	}
	// Mono passes through unchanged.
	mono := []float64{0.1, 0.2}
	if out := downmixMono(mono, 1); len(out) != 2 || out[0] != 0.1 {
		t.Errorf("mono passthrough altered: %v", out)
	}
}

func TestResampleLinearLength(t *testing.T) {
	// A known ramp resampled 22050 → 44100 doubles in length (round rule).
	in := make([]float64, 100)
	for i := range in {
		in[i] = float64(i) / 100.0
	}
	out := resampleLinear(in, 22050, 44100)
	want := int(math.Round(float64(len(in)) * 44100.0 / 22050.0))
	if len(out) != want {
		t.Fatalf("resample length %d, want %d", len(out), want)
	}
	// Equal rates pass through unchanged.
	if same := resampleLinear(in, 44100, 44100); len(same) != len(in) {
		t.Errorf("equal-rate resample changed length: %d", len(same))
	}
	// Downsample 44100 → 22050 halves length.
	down := resampleLinear(in, 44100, 22050)
	if wantDown := int(math.Round(float64(len(in)) * 0.5)); len(down) != wantDown {
		t.Errorf("downsample length %d, want %d", len(down), wantDown)
	}
}

func TestTrimSilence(t *testing.T) {
	in := []float64{0, 0, 0.5, -0.5, 0, 0}
	got := trimSilence(in)
	if len(got) != 2 || got[0] != 0.5 || got[1] != -0.5 {
		t.Fatalf("trim = %v, want [0.5 -0.5]", got)
	}
	// All-silent collapses to a single frame (never empty).
	if s := trimSilence([]float64{0, 0, 0}); len(s) != 1 {
		t.Errorf("all-silent trim = %v, want length 1", s)
	}
}

func TestNormalizePeak(t *testing.T) {
	in := []float64{0.25, -0.5, 0.1}
	out := normalizePeak(in, 0.0) // target 0 dBFS → peak becomes 1.0
	var peak float64
	for _, v := range out {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if math.Abs(peak-1.0) > 1e-9 {
		t.Fatalf("peak after normalize = %v, want 1.0", peak)
	}
	// Silent buffer is untouched (no divide-by-zero).
	if s := normalizePeak([]float64{0, 0}, -1.0); s[0] != 0 {
		t.Errorf("silent normalize altered buffer: %v", s)
	}
}

func TestTranscode24to16MonoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// 24-bit stereo 44100 fixture: two frames.
	// Frame0: L=+quarter, R=+quarter → mono +quarter. Frame1: L=-quarter, R=-quarter.
	q := 1 << 21 // ~0.25 of full-scale 24-bit (2^23)
	src := writeTestWAV(t, dir, 44100, 2, 24, []int{q, q, -q, -q})

	out, err := Transcode(src, TranscodeOptions{}) // no trim/normalize: check raw conversion
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	rate, ch, bits, data := decodeWAVBytes(t, dir, out)
	if rate != 44100 || ch != 1 || bits != 16 {
		t.Fatalf("output format %dHz/%dch/%dbit, want 44100/1/16", rate, ch, bits)
	}
	if len(data) != 2 {
		t.Fatalf("output frames %d, want 2", len(data))
	}
	// 0.25 * 32768 = 8192, within one LSB.
	if diff := math.Abs(float64(data[0]) - 8192); diff > 1 {
		t.Errorf("sample0 = %d, want ~8192", data[0])
	}
	if diff := math.Abs(float64(data[1]) - (-8192)); diff > 1 {
		t.Errorf("sample1 = %d, want ~-8192", data[1])
	}
}

func TestTranscodeDeterministic(t *testing.T) {
	dir := t.TempDir()
	src := writeTestWAV(t, dir, 44100, 1, 16, []int{100, -200, 300, -400, 500})
	opts := TranscodeOptions{TrimSilence: true, NormalizePeakDBFS: fptr(-1.0)}
	a, err := Transcode(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Transcode(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("transcode output not byte-identical across runs")
	}
}

func fptr(f float64) *float64 { return &f }
