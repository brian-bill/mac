package audio

import (
	"fmt"
	"os"
	"sort"

	"github.com/bryanbill/mac/internal/schedule"
)

// Render loads the wav for every instrument referenced in events, mixes them,
// and writes the encoded MP3 to outputPath (overwriting an existing file).
// instrumentPaths maps an instrument ID to its .wav path on disk; only
// instruments actually referenced by events are loaded (each loaded once, even
// if it fires many times).
//
// On any error before a successful close, a partial output file may be left on
// disk (atomic temp-file + rename is out of scope, deferred to a later spec).
func Render(events []schedule.AudioEvent, instrumentPaths map[string]string, outputPath string) error {
	referenced := referencedInstruments(events)

	var missing []string
	samples := make(map[string]*Sample, len(referenced))
	for _, id := range referenced {
		path, ok := instrumentPaths[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		s, err := LoadWAV(path)
		if err != nil {
			return err
		}
		samples[id] = s
	}
	if len(missing) > 0 {
		return fmt.Errorf("unknown instrument(s) in schedule: %s (no path provided)", quoteList(missing))
	}

	mix, err := Mix(events, samples)
	if err != nil {
		return err
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("render %s: %w", outputPath, err)
	}
	defer f.Close()

	if err := EncodeMP3(f, mix); err != nil {
		return fmt.Errorf("render %s: %w", outputPath, err)
	}
	return nil
}

// referencedInstruments returns the sorted, de-duplicated set of instrument IDs
// that appear in events. Sorting makes the load order (and thus any error
// ordering) deterministic.
func referencedInstruments(events []schedule.AudioEvent) []string {
	seen := make(map[string]struct{})
	for _, ev := range events {
		seen[ev.Instrument] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
