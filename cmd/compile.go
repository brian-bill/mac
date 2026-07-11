package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/bryanbill/mac/internal/audio"
	"github.com/bryanbill/mac/internal/bt"
	"github.com/bryanbill/mac/internal/db"
	"github.com/bryanbill/mac/internal/instruments"
	"github.com/bryanbill/mac/internal/schedule"
	"github.com/spf13/cobra"
)

// outputPath is the target audio file for the compile command (-o/--output).
var outputPath string

var compileCmd = &cobra.Command{
	Use:   "compile [beats-directory]",
	Short: "Compile a directory of .bt files into an audio file",
	Long: `compile parses every .bt file in the given beats directory and renders
them into a single audio file.

Each .bt file is one section; sections concatenate in sorted filename order to
form a continuous piece. The rendered output is a real, playable .mp3 file.`,
	Args: cobra.ExactArgs(1),
	RunE: runCompile,
}

func init() {
	compileCmd.Flags().StringVarP(&outputPath, "output", "o", "output.mp3",
		"path to the rendered audio output file")
	rootCmd.AddCommand(compileCmd)
}

// instrumentConfig decodes the "config" JSON blob of a registered instrument.
// Only the velocity map is consumed by the compile pipeline; instruments with
// no velocity key simply contribute no entries (unknown velocity letters error
// loudly in the scheduler rather than being silently guessed).
type instrumentConfig struct {
	Velocity map[string]float64 `json:"velocity"`
}

// runCompile is the RunE handler for the compile command. It chains the four
// leaf layers — parse, schedule, resolve, render — into one pipeline:
//
//	bt.ParseFile → schedule.Schedule → db.ListInstruments → audio.Render
//
// See specs/007-compile-pipeline-integration.md for the full design.
func runCompile(cmd *cobra.Command, args []string) error {
	beatsDir, err := validateBeatsDir(args[0])
	if err != nil {
		return err
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()

	// Register the instrument library. A missing instruments directory is not an
	// error — a project may not have declared any instruments yet — so treat it
	// as an empty library and carry on.
	if _, err := os.Stat(instrumentsPath); err == nil {
		if _, err := instruments.Register(database, instrumentsPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat instruments directory %q: %w", instrumentsPath, err)
	}

	// Read the registry once and build the (ID → wav path) and (ID → velocity
	// map) lookups the render and schedule steps need.
	rows, err := db.ListInstruments(database)
	if err != nil {
		return err
	}
	paths, velocities, err := loadInstrumentTables(rows)
	if err != nil {
		return err
	}
	vel := velocityFunc(velocities)

	// Discover and lexically sort .bt files; the sort is the load-bearing
	// determinism choice for multi-file compilation.
	files, err := findBeatFiles(beatsDir)
	if err != nil {
		return err
	}

	// Parse + schedule each file, concatenating sections: each file's events are
	// offset by the cumulative bar time (in ms) of the already-processed files.
	// The first file's cursor is 0, so the single-file case is offset-free.
	var allEvents []schedule.AudioEvent
	var cursor float64
	for _, path := range files {
		events, barDur, err := parseAndSchedule(path, vel)
		if err != nil {
			return err
		}
		for i := range events {
			allEvents = append(allEvents, schedule.AudioEvent{
				TimeMs:     events[i].TimeMs + cursor,
				Instrument: events[i].Instrument,
				Velocity:   events[i].Velocity,
			})
		}
		cursor += barDur
	}

	// Render: loads only the referenced instruments' wavs (each once), mixes,
	// and encodes to outputPath.
	if err := audio.Render(allEvents, paths, outputPath); err != nil {
		return err
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("stat rendered output %s: %w", outputPath, err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "beats directory:        %s\n", beatsDir)
	fmt.Fprintf(out, "instruments directory:  %s\n", instrumentsPath)
	fmt.Fprintf(out, "output target:          %s\n", outputPath)
	fmt.Fprintf(out, "registered instruments: %d\n", len(rows))
	fmt.Fprintf(out, "rendered %s (%d bytes)\n", outputPath, info.Size())
	return nil
}

// findBeatFiles walks dir recursively, collecting every *.bt entry, and returns
// them sorted lexically by full path. The sort is the load-bearing
// determinism choice for multi-file compilation (sections concatenate in
// filename order). An empty beats directory is an error, not a silent empty
// render — a user who points compile at the wrong directory should hear about
// it.
func findBeatFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".bt" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning beats directory %q: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .bt files found in %s", dir)
	}
	sort.Strings(files)
	return files, nil
}

// loadInstrumentTables builds two lookups from a single db.ListInstruments
// query result:
//
//   - paths: instrument ID → wav file path on disk (fed to audio.Render).
//   - velocities: instrument ID → {symbol → gain}, decoded from each
//     instrument's Config JSON "velocity" object. Instruments with no
//     "velocity" key contribute no entries.
//
// In practice the config JSON is always valid (instruments.Register's
// normalizeConfig validated it on the way in), but a malformed blob is reported
// defensively.
func loadInstrumentTables(rows []db.Instrument) (paths map[string]string, velocities map[string]map[string]float64, err error) {
	paths = make(map[string]string, len(rows))
	velocities = make(map[string]map[string]float64, len(rows))
	for _, ins := range rows {
		paths[ins.ID] = ins.SamplePath
		var cfg instrumentConfig
		if err := json.Unmarshal([]byte(ins.Config), &cfg); err != nil {
			return nil, nil, fmt.Errorf("decode config for instrument %q: %w", ins.ID, err)
		}
		if len(cfg.Velocity) > 0 {
			velocities[ins.ID] = cfg.Velocity
		}
	}
	return paths, velocities, nil
}

// parseAndSchedule parses a single .bt file and schedules its events, returning
// the events, the file's bar duration in milliseconds, and any error. Parse
// and schedule errors are surfaced verbatim (they are bt.ErrorList values that
// already render as "file:line:col: msg").
func parseAndSchedule(path string, vel schedule.VelocityFunc) ([]schedule.AudioEvent, float64, error) {
	comp, err := bt.ParseFile(path)
	if err != nil {
		return nil, 0, err
	}
	events, err := schedule.Schedule(filepath.Base(path), comp, vel)
	if err != nil {
		return nil, 0, err
	}
	return events, barMsV2(comp), nil
}

// barMsV2 returns the duration of one bar in milliseconds. It mirrors
// internal/schedule.barMs exactly; the formula is recomputed locally to avoid
// exporting a helper from the frozen scheduler package. If the two ever diverge
// it is a bug.
func barMsV2(comp *bt.Composition) float64 {
	beatMs := 60000.0 / float64(comp.BPM)
	return float64(comp.Signature.Numerator) * beatMs
}

// velocityFunc builds a schedule.VelocityFunc closure over the registered
// instruments' velocity maps. Returns nil-safe behavior: an instrument with no
// velocity map (or an entirely empty maps argument) yields ok=false, which the
// scheduler turns into a positioned error only if a letter hit actually
// references it.
func velocityFunc(maps map[string]map[string]float64) schedule.VelocityFunc {
	return func(instrument, symbol string) (gain float64, ok bool) {
		m, ok := maps[instrument]
		if !ok {
			return 0, false
		}
		gain, ok = m[symbol]
		return gain, ok
	}
}

// validateBeatsDir resolves dir to an absolute path and verifies it is an
// existing directory, returning descriptive errors otherwise.
func validateBeatsDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve beats directory %q: %w", dir, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("beats directory does not exist: %s", abs)
		}
		return "", fmt.Errorf("stat beats directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", abs)
	}
	return abs, nil
}
