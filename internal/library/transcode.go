package library

import (
	"fmt"
	"math"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// Canonical audio format for the whole vendored library. Every sample is
// normalized to this so the mixer's canonicalFormat check (internal/audio) —
// which rejects any render whose samples disagree on rate or channels — always
// passes across the entire default library. See specs/006-default-sound-library.md.
const (
	canonicalSampleRate = 44100
	canonicalChannels   = 1
	canonicalBitDepth   = 16

	// wavFormatPCM is the WAV audio-format tag for linear PCM.
	wavFormatPCM = 1
)

// TranscodeOptions controls the optional post-resample processing applied while
// normalizing a sample. Downmix-to-mono, resample-to-44100, and requantize-to-16-bit
// are always applied (they are required to reach the canonical format); the
// fields here are opt-in per catalog entry.
type TranscodeOptions struct {
	// NormalizePeakDBFS, when non-nil, scales the sample so its peak sits at the
	// given dBFS level (e.g. -1.0). Silent samples are left untouched.
	NormalizePeakDBFS *float64
	// TrimSilence, when true, removes leading and trailing near-silence (below
	// trimThreshold) from the sample.
	TrimSilence bool
}

// trimThreshold is the absolute normalized amplitude below which a frame is
// considered silent for TrimSilence. ~-60 dBFS.
const trimThreshold = 0.001

// Transcode reads the PCM WAV at srcPath, normalizes it to the canonical format
// (mono / 44100 Hz / 16-bit PCM), applies the opt-in transforms, and returns the
// encoded canonical WAV bytes.
//
// The pipeline is pure Go and deterministic: identical input bytes and options
// always yield identical output bytes, so the committed SHA-256 in the catalog
// is stable. Only PCM WAV input is accepted (mirroring the engine's LoadWAV);
// other container formats must be pre-converted before they reach the catalog.
func Transcode(srcPath string, opts TranscodeOptions) ([]byte, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("transcode %s: %w", srcPath, err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("transcode %s: not a valid WAV file", srcPath)
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, fmt.Errorf("transcode %s: %w", srcPath, err)
	}
	if buf.Format == nil {
		return nil, fmt.Errorf("transcode %s: missing WAV format", srcPath)
	}

	srcRate := buf.Format.SampleRate
	srcChannels := buf.Format.NumChannels
	bitDepth := buf.SourceBitDepth
	if srcRate <= 0 {
		return nil, fmt.Errorf("transcode %s: invalid sample rate %d", srcPath, srcRate)
	}
	if srcChannels < 1 {
		return nil, fmt.Errorf("transcode %s: invalid channel count %d", srcPath, srcChannels)
	}
	if bitDepth != 16 && bitDepth != 24 && bitDepth != 32 {
		return nil, fmt.Errorf("transcode %s: unsupported bit depth %d (want 16/24/32)", srcPath, bitDepth)
	}
	if len(buf.Data) == 0 {
		return nil, fmt.Errorf("transcode %s: empty WAV", srcPath)
	}

	// 1. Integer PCM → normalized float64 in [-1, 1].
	floats := toFloat(buf.Data, bitDepth)

	// 2. Downmix to mono (averaging channels).
	mono := downmixMono(floats, srcChannels)

	// 3. Resample to the canonical rate.
	resampled := resampleLinear(mono, srcRate, canonicalSampleRate)

	// 4. Opt-in trim, then opt-in peak-normalize (order matters: trim first so
	//    the peak is measured on the retained signal).
	if opts.TrimSilence {
		resampled = trimSilence(resampled)
	}
	if opts.NormalizePeakDBFS != nil {
		resampled = normalizePeak(resampled, *opts.NormalizePeakDBFS)
	}

	// 5. Float → 16-bit PCM, encode as canonical WAV.
	return encodeWAV16(resampled)
}

// toFloat converts signed integer PCM samples to float64 in [-1, 1) by dividing
// by 2^(bitDepth-1), matching internal/audio's normalize so a round trip is
// lossless to within int truncation.
func toFloat(data []int, bitDepth int) []float64 {
	div := float64(int64(1) << uint(bitDepth-1))
	out := make([]float64, len(data))
	for i, v := range data {
		out[i] = float64(v) / div
	}
	return out
}

// downmixMono collapses interleaved multi-channel float PCM to mono by averaging
// the channels of each frame. Mono input is returned unchanged.
func downmixMono(interleaved []float64, channels int) []float64 {
	if channels <= 1 {
		return interleaved
	}
	frames := len(interleaved) / channels
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < channels; c++ {
			sum += interleaved[i*channels+c]
		}
		out[i] = sum / float64(channels)
	}
	return out
}

// resampleLinear resamples mono samples from srcRate to dstRate using linear
// interpolation. Equal rates return the input unchanged. The output length is
// round(len(in) * dstRate/srcRate), the deterministic mapping the spec's test
// asserts.
func resampleLinear(in []float64, srcRate, dstRate int) []float64 {
	if srcRate == dstRate || len(in) == 0 {
		return in
	}
	ratio := float64(dstRate) / float64(srcRate)
	outLen := int(math.Round(float64(len(in)) * ratio))
	if outLen <= 0 {
		return []float64{}
	}
	out := make([]float64, outLen)
	// Position step in input samples per output sample.
	step := float64(srcRate) / float64(dstRate)
	for i := 0; i < outLen; i++ {
		pos := float64(i) * step
		idx := int(math.Floor(pos))
		frac := pos - float64(idx)
		if idx+1 < len(in) {
			out[i] = in[idx]*(1-frac) + in[idx+1]*frac
		} else {
			out[i] = in[len(in)-1]
		}
	}
	return out
}

// trimSilence removes leading and trailing frames whose absolute amplitude is
// below trimThreshold. An all-silent buffer collapses to empty-but-nonzero (a
// single frame) so downstream encoding never sees a zero-length sample.
func trimSilence(in []float64) []float64 {
	start := 0
	for start < len(in) && math.Abs(in[start]) < trimThreshold {
		start++
	}
	end := len(in)
	for end > start && math.Abs(in[end-1]) < trimThreshold {
		end--
	}
	if start >= end {
		// Entirely silent: keep a single frame so the WAV is non-empty.
		return []float64{0}
	}
	return in[start:end]
}

// normalizePeak scales in so its peak amplitude sits at targetDBFS. A silent
// buffer (peak 0) is returned unchanged to avoid division by zero.
func normalizePeak(in []float64, targetDBFS float64) []float64 {
	var peak float64
	for _, v := range in {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if peak == 0 {
		return in
	}
	target := math.Pow(10, targetDBFS/20.0)
	gain := target / peak
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * gain
	}
	return out
}

// encodeWAV16 encodes mono float samples as a canonical 44100/1/16-bit PCM WAV
// and returns the bytes. Floats are hard-clipped to the int16 range with the
// same rounding as the engine's floatToInt16 so a decode round trip is exact.
func encodeWAV16(samples []float64) ([]byte, error) {
	ints := make([]int, len(samples))
	for i, v := range samples {
		ints[i] = int(floatToInt16(v))
	}

	ib := &audio.IntBuffer{
		Format:         &audio.Format{SampleRate: canonicalSampleRate, NumChannels: canonicalChannels},
		Data:           ints,
		SourceBitDepth: canonicalBitDepth,
	}

	ws := newWriteSeekBuffer()
	enc := wav.NewEncoder(ws, canonicalSampleRate, canonicalBitDepth, canonicalChannels, wavFormatPCM)
	if err := enc.Write(ib); err != nil {
		return nil, fmt.Errorf("encode canonical wav: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("finalize canonical wav: %w", err)
	}
	return ws.Bytes(), nil
}

// floatToInt16 converts a normalized float64 sample to int16 with hard clipping,
// identical to internal/audio.floatToInt16 (duplicated to avoid importing the
// audio package into the build-time library tool).
func floatToInt16(v float64) int16 {
	scaled := v * 32768.0
	if scaled >= 32767.0 {
		return 32767
	}
	if scaled <= -32768.0 {
		return -32768
	}
	return int16(math.Round(scaled))
}

// writeSeekBuffer is an in-memory io.WriteSeeker, required because wav.Encoder
// seeks back to backfill chunk sizes. bytes.Buffer alone cannot seek.
type writeSeekBuffer struct {
	buf []byte
	pos int64
}

func newWriteSeekBuffer() *writeSeekBuffer { return &writeSeekBuffer{} }

func (w *writeSeekBuffer) Write(p []byte) (int, error) {
	end := w.pos + int64(len(p))
	if end > int64(len(w.buf)) {
		grown := make([]byte, end)
		copy(grown, w.buf)
		w.buf = grown
	}
	copy(w.buf[w.pos:end], p)
	w.pos = end
	return len(p), nil
}

func (w *writeSeekBuffer) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case 0: // io.SeekStart
		next = offset
	case 1: // io.SeekCurrent
		next = w.pos + offset
	case 2: // io.SeekEnd
		next = int64(len(w.buf)) + offset
	default:
		return 0, fmt.Errorf("invalid seek whence %d", whence)
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek position %d", next)
	}
	w.pos = next
	return next, nil
}

// Bytes returns the fully written buffer.
func (w *writeSeekBuffer) Bytes() []byte {
	return append([]byte(nil), w.buf...)
}
