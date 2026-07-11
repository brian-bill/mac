package audio

import (
	"io"
	"math"

	mp3 "github.com/braheezy/shine-mp3/pkg/mp3"
)

// EncodeMP3 converts mix to 16-bit interleaved PCM (hard-clipped to the int16
// range) and writes it as MPEG-1 Layer III to w using the pure-Go shine
// encoder. The MP3 sample rate and channel count are taken from mix.
//
// A zero-length mix.Data still calls the encoder (which emits a valid stream);
// EncodeMP3 does not special-case emptiness.
func EncodeMP3(w io.Writer, mix *MixBuffer) error {
	pcm := make([]int16, len(mix.Data))
	for i, v := range mix.Data {
		pcm[i] = floatToInt16(v)
	}
	enc := mp3.NewEncoder(mix.SampleRate, mix.NumChannels)
	return enc.Write(w, pcm)
}

// floatToInt16 converts a normalized float64 sample to int16 with hard clipping
// (saturation) at the boundaries. Scaling uses the canonical 32768 divisor's
// inverse; math.Round makes the conversion deterministic and platform-stable.
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
