package audio

import (
	"fmt"
	"math"
	"sort"

	"github.com/bryanbill/mac/internal/schedule"
)

// defaultSampleRate and defaultChannels are the format of an empty Mix (no
// samples present). Only reached when events is also empty; any non-empty event
// with an empty map fails the missing-instrument check first.
const (
	defaultSampleRate = 44100
	defaultChannels   = 1
)

// MixBuffer is the mixed, interleaved float64 PCM result of combining events,
// prior to int conversion/encoding. It carries the shared format so the encoder
// knows the sample rate and channel count. Data may exceed [-1,1] before
// clipping.
//
// (The type is named MixBuffer rather than Mix because Go cannot share an
// identifier between a type and the headline Mix function that returns it.)
type MixBuffer struct {
	Data        []float64
	SampleRate  int
	NumChannels int
}

// Mix combines events into a single buffer. samples maps an instrument ID (the
// AudioEvent.Instrument value) to its loaded Sample. All samples must share the
// same SampleRate and NumChannels; the first sample (by sorted key) fixes the
// canonical format and any disagreement is an error.
//
// Each event places samples[event.Instrument] starting at frame
// round(event.TimeMs/1000 * SampleRate), with every value scaled by
// event.Velocity, summed into the accumulation buffer (which grows to fit the
// last-ending sample). Events are processed in slice order; because the
// schedule is already totally ordered (Spec 004) and float addition order is
// fixed here, the output is deterministic.
//
// Returns an error if any event references an instrument absent from samples,
// or if two samples disagree on format. An empty events slice yields a valid
// zero-length Mix (with the canonical format if any sample is present, else a
// default 44100/1).
func Mix(events []schedule.AudioEvent, samples map[string]*Sample) (*MixBuffer, error) {
	if missing := missingInstruments(events, samples); len(missing) > 0 {
		return nil, fmt.Errorf("unknown instrument(s) in schedule: %s (no sample provided)", quoteList(missing))
	}

	rate, channels, err := canonicalFormat(samples)
	if err != nil {
		return nil, err
	}

	var acc []float64
	for _, ev := range events {
		s := samples[ev.Instrument]
		frame := int(math.Round(ev.TimeMs / 1000.0 * float64(rate)))
		base := frame * channels
		end := base + len(s.Data)
		if end > len(acc) {
			acc = append(acc, make([]float64, end-len(acc))...)
		}
		for j, v := range s.Data {
			acc[base+j] += v * ev.Velocity
		}
	}

	if acc == nil {
		acc = []float64{}
	}
	return &MixBuffer{Data: acc, SampleRate: rate, NumChannels: channels}, nil
}

// missingInstruments returns the sorted, de-duplicated set of instruments
// referenced by events that are absent from samples.
func missingInstruments(events []schedule.AudioEvent, samples map[string]*Sample) []string {
	seen := make(map[string]struct{})
	for _, ev := range events {
		if _, ok := samples[ev.Instrument]; !ok {
			seen[ev.Instrument] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// canonicalFormat picks the shared (sampleRate, channels) for the render. All
// samples must agree; the smallest instrument key fixes the reference so the
// mismatch error is deterministic. An empty map yields the default format.
func canonicalFormat(samples map[string]*Sample) (rate, channels int, err error) {
	if len(samples) == 0 {
		return defaultSampleRate, defaultChannels, nil
	}

	ids := make([]string, 0, len(samples))
	for id := range samples {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	ref := ids[0]
	rate = samples[ref].SampleRate
	channels = samples[ref].NumChannels
	for _, id := range ids[1:] {
		s := samples[id]
		if s.SampleRate != rate || s.NumChannels != channels {
			return 0, 0, fmt.Errorf(
				"sample format mismatch: instrument %q is %dHz/%dch but %q is %dHz/%dch",
				ref, rate, channels, id, s.SampleRate, s.NumChannels)
		}
	}
	return rate, channels, nil
}

// quoteList renders ids as a comma-separated list of quoted strings for error
// messages, e.g. `"Clap", "Tom"`.
func quoteList(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	return joinComma(quoted)
}

// joinComma joins parts with ", " (avoids importing strings for one call).
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
