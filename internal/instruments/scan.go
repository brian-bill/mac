package instruments

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/bryanbill/mac/internal/db"
)

// Scan walks root recursively, discovering every instrument folder (a directory
// containing a manifest.json), parsing and validating each manifest, and
// returning the resulting instruments sorted by ID.
//
// Scanning is fail-fast: a single malformed manifest or a duplicate ID aborts
// the whole scan with a descriptive error, so a broken library never registers
// a partial set. Scan does not touch the database.
func Scan(root string) ([]db.Instrument, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve instruments directory %q: %w", root, err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("instruments directory does not exist: %s", absRoot)
		}
		return nil, fmt.Errorf("stat instruments directory %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", absRoot)
	}

	// Collect instrument folders first, in deterministic lexical order.
	var manifestDirs []string
	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == manifestFileName {
			manifestDirs = append(manifestDirs, filepath.Dir(path))
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scan instruments directory %s: %w", absRoot, walkErr)
	}
	sort.Strings(manifestDirs)

	// seenIDs maps an instrument ID to the manifest path that first declared it,
	// so a duplicate can name both sources.
	seenIDs := make(map[string]string, len(manifestDirs))
	instruments := make([]db.Instrument, 0, len(manifestDirs))

	for _, dir := range manifestDirs {
		manifestPath := filepath.Join(dir, manifestFileName)

		m, err := ParseManifest(manifestPath)
		if err != nil {
			return nil, err
		}

		samplePath, configJSON, err := m.validate(dir)
		if err != nil {
			return nil, err
		}

		if prev, ok := seenIDs[m.ID]; ok {
			return nil, fmt.Errorf(
				"duplicate instrument id %q declared in %s and %s",
				m.ID, prev, manifestPath,
			)
		}
		seenIDs[m.ID] = manifestPath

		instruments = append(instruments, db.Instrument{
			ID:         m.ID,
			Name:       m.Name,
			SamplePath: samplePath,
			Config:     configJSON,
		})
	}

	// Instruments are appended in manifestDirs (lexical) order, which is not
	// necessarily ID order; sort by ID for a stable, predictable result.
	sort.Slice(instruments, func(i, j int) bool {
		return instruments[i].ID < instruments[j].ID
	})

	return instruments, nil
}

// Register scans root and writes every discovered instrument into the registry
// within a single transaction, returning the number registered. If any step
// fails the transaction is rolled back, leaving the registry unchanged, so a
// failed scan is atomic. Re-running Register on an unchanged directory is
// idempotent: existing rows are updated in place via UPSERT rather than
// duplicated.
func Register(database *sql.DB, root string) (registered int, err error) {
	instruments, err := Scan(root)
	if err != nil {
		return 0, err
	}

	tx, err := database.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin registration transaction: %w", err)
	}
	// Roll back on any early return; the Commit below makes this a no-op on the
	// success path.
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, ins := range instruments {
		if err = db.UpsertInstrument(tx, ins); err != nil {
			return 0, err
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit registration transaction: %w", err)
	}

	return len(instruments), nil
}
