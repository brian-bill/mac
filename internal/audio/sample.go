// Package audio is the mac audio engine: it turns a []schedule.AudioEvent
// (from package schedule) into mixed PCM and encodes it to MP3.
//
// The pipeline is three stages, each a public entry point:
//
//	LoadWAV   decode a .wav file into a normalized float Sample
//	Mix       sum scheduled Samples into one float64 buffer at their timestamps
//	EncodeMP3 clip the float mix to int16 and encode it as MPEG-1 Layer III
//
// Render chains all three: events + (instrument → wav path) → output.mp3.
//
// The package is DB-free, mirroring the leaf discipline of internal/bt,
// internal/instruments, and internal/schedule: the instrument→wav-path mapping
// arrives injected as a map[string]string rather than by reaching into the
// registry. It uses no time-based sequencing — an event's timestamp is treated
// purely as data and converted to a sample-frame index arithmetically — so the
// same schedule and samples always produce byte-identical output (GOAL.md §4).
package audio

import (
	"fmt"
	"os"

	"github.com/go-audio/wav"
)

// Sample is one decoded .wav file as normalized, interleaved PCM.
//
// Data holds float64 samples in [-1.0, 1.0], interleaved by channel
// (L,R,L,R,… for stereo; one value per frame for mono). SampleRate and
// NumChannels are carried from the source file; the mixer requires every
// Sample in a render to agree on both.
type Sample struct {
	Data        []float64
	SampleRate  int
	NumChannels int
}

// Frames returns the number of sample frames (Data length / channels).
func (s *Sample) Frames() int { return len(s.Data) / s.NumChannels }

// LoadWAV reads and decodes the PCM WAV file at path into a normalized Sample.
// It supports 16/24/32-bit signed PCM WAV; 8-bit (unsigned) PCM and non-PCM
// (e.g. float, compressed) files are rejected with a descriptive error. The
// file is fully decoded into memory (samples are short one-shots).
//
// All errors are wrapped with the path so diagnostics name the offending file.
func LoadWAV(path string) (*Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load wav %s: %w", path, err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("load wav %s: not a valid WAV file", path)
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, fmt.Errorf("load wav %s: %w", path, err)
	}

	if buf.Format == nil {
		return nil, fmt.Errorf("load wav %s: missing format", path)
	}
	bitDepth := buf.SourceBitDepth
	sampleRate := buf.Format.SampleRate
	numChannels := buf.Format.NumChannels

	if bitDepth == 8 {
		return nil, fmt.Errorf("load wav %s: 8-bit WAV is not supported", path)
	}
	if bitDepth != 16 && bitDepth != 24 && bitDepth != 32 {
		return nil, fmt.Errorf("load wav %s: unsupported bit depth %d", path, bitDepth)
	}
	if numChannels < 1 {
		return nil, fmt.Errorf("load wav %s: invalid channel count %d", path, numChannels)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("load wav %s: invalid sample rate %d", path, sampleRate)
	}
	if len(buf.Data) == 0 {
		return nil, fmt.Errorf("load wav %s: empty WAV", path)
	}

	return &Sample{
		Data:        normalize(buf.Data, bitDepth),
		SampleRate:  sampleRate,
		NumChannels: numChannels,
	}, nil
}

// normalize maps signed integer PCM samples to float64 in [-1.0, 1.0) by
// dividing by the canonical divisor for the bit depth. The same divisor is used
// in reverse on encode (see floatToInt16), so a non-clipping round trip is
// lossless to within int truncation.
func normalize(data []int, bitDepth int) []float64 {
	div := scaleForBitDepth(bitDepth)
	out := make([]float64, len(data))
	for i, v := range data {
		out[i] = float64(v) / div
	}
	return out
}

// scaleForBitDepth returns the canonical normalization divisor 2^(bitDepth-1):
// 32768 for 16-bit, 8388608 for 24-bit, 2147483648 for 32-bit.
func scaleForBitDepth(bitDepth int) float64 {
	return float64(int64(1) << uint(bitDepth-1))
}
