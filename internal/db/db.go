// Package db manages the local SQLite instrument registry.
//
// The registry stores the metadata, sample paths, and user configuration for
// every instrument that a .bt track can reference. It uses the CGO-free
// modernc.org/sqlite driver so the CLI builds and runs without a C toolchain.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver with database/sql
)

// Instrument is a single row in the instrument registry. Config holds the raw
// JSON configuration blob exactly as it will be stored (the caller is
// responsible for serialization and validation).
type Instrument struct {
	ID         string
	Name       string
	SamplePath string
	Config     string
}

// schemaSQL holds the DDL for the instrument registry, embedded into the
// binary so the schema travels with the executable.
//
//go:embed schema.sql
var schemaSQL string

// Open opens (creating if necessary) the SQLite registry at path, verifies the
// connection, and applies the embedded schema. The returned *sql.DB is ready
// for use; the caller is responsible for calling Close on it.
func Open(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}

	// SQLite is a single-writer database. Constraining the pool to one
	// connection avoids SQLITE_BUSY errors under concurrent access with the
	// pure-Go driver.
	database.SetMaxOpenConns(1)

	// Ping fails fast on a bad path or unwritable location, before we attempt
	// to run the schema.
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("connect to sqlite database %q: %w", path, err)
	}

	// The schema uses IF NOT EXISTS, so applying it on every open is a
	// harmless no-op once the tables already exist.
	if _, err := database.Exec(schemaSQL); err != nil {
		database.Close()
		return nil, fmt.Errorf("apply schema to %q: %w", path, err)
	}

	// Guarded schema bump: databases created before updated_at was introduced
	// still have the CREATE TABLE IF NOT EXISTS run above as a no-op, leaving
	// them without the column. Add it here, tolerating the "duplicate column
	// name" error that fresh databases (which already have it) produce. This
	// keeps Open idempotent across the schema change without pulling in a
	// migration framework.
	//
	// SQLite forbids ADD COLUMN with a non-constant default (CURRENT_TIMESTAMP),
	// so the backfill for pre-existing rows uses a constant epoch sentinel;
	// subsequent UpsertInstrument calls set updated_at to CURRENT_TIMESTAMP.
	if _, err := database.Exec(
		`ALTER TABLE instruments ADD COLUMN updated_at DATETIME NOT NULL DEFAULT '1970-01-01 00:00:00'`,
	); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		database.Close()
		return nil, fmt.Errorf("migrate schema of %q: %w", path, err)
	}

	return database, nil
}

// CountInstruments returns the number of registered instruments. It exists
// primarily to prove the connection round-trips against a real query.
func CountInstruments(database *sql.DB) (int, error) {
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM instruments").Scan(&count); err != nil {
		return 0, fmt.Errorf("count instruments: %w", err)
	}
	return count, nil
}

// UpsertInstrument inserts ins, or updates the existing row when an instrument
// with the same ID is already registered. It runs within the caller's
// transaction so an entire library scan registers atomically. On conflict the
// mutable fields are overwritten and updated_at is bumped, while created_at is
// preserved.
func UpsertInstrument(tx *sql.Tx, ins Instrument) error {
	_, err := tx.Exec(
		`INSERT INTO instruments (id, name, sample_path, config, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET
		     name=excluded.name,
		     sample_path=excluded.sample_path,
		     config=excluded.config,
		     updated_at=CURRENT_TIMESTAMP`,
		ins.ID, ins.Name, ins.SamplePath, ins.Config,
	)
	if err != nil {
		return fmt.Errorf("upsert instrument %q: %w", ins.ID, err)
	}
	return nil
}

// ListInstruments returns every registered instrument ordered by ID, giving
// callers (and tests) a deterministic view of the registry.
func ListInstruments(database *sql.DB) ([]Instrument, error) {
	rows, err := database.Query(
		`SELECT id, name, sample_path, config FROM instruments ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	defer rows.Close()

	var instruments []Instrument
	for rows.Next() {
		var ins Instrument
		if err := rows.Scan(&ins.ID, &ins.Name, &ins.SamplePath, &ins.Config); err != nil {
			return nil, fmt.Errorf("scan instrument row: %w", err)
		}
		instruments = append(instruments, ins)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate instrument rows: %w", err)
	}
	return instruments, nil
}
