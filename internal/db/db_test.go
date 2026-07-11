package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	tests := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr bool
	}{
		{
			name:    "happy path creates database",
			path:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "mac.db") },
			wantErr: false,
		},
		{
			name:    "path inside non-existent directory fails",
			path:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing", "mac.db") },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database, err := Open(tt.path(t))
			if tt.wantErr {
				if err == nil {
					database.Close()
					t.Fatalf("Open() = nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Open() error = %v, want nil", err)
			}
			if database == nil {
				t.Fatal("Open() returned nil *sql.DB")
			}
			database.Close()
		})
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mac.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v (schema not idempotent?)", err)
	}
	second.Close()
}

func TestCountInstruments(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	count, err := CountInstruments(database)
	if err != nil {
		t.Fatalf("CountInstruments() error = %v", err)
	}
	if count != 0 {
		t.Fatalf("CountInstruments() = %d, want 0", count)
	}

	_, err = database.Exec(
		`INSERT INTO instruments (id, name, sample_path) VALUES (?, ?, ?)`,
		"Kick", "Kick Drum", "/samples/kick.wav",
	)
	if err != nil {
		t.Fatalf("insert instrument error = %v", err)
	}

	count, err = CountInstruments(database)
	if err != nil {
		t.Fatalf("CountInstruments() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CountInstruments() = %d, want 1", count)
	}
}

func TestCountInstrumentsError(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	// Drop the table so the COUNT query fails, exercising the error path.
	if _, err := database.Exec(`DROP TABLE instruments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	if _, err := CountInstruments(database); err == nil {
		t.Fatal("CountInstruments() = nil error, want error after table dropped")
	}
}

func TestOpenInvalidDatabaseFile(t *testing.T) {
	// A directory cannot be opened as a SQLite file: Ping/Exec should fail.
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Fatal("Open(directory) = nil error, want error")
	}
}

func TestOpenSchemaExecFails(t *testing.T) {
	// Seed a valid but empty SQLite file (no instruments table) using the raw
	// driver, then reopen it read-only via Open. Applying the schema requires a
	// CREATE TABLE write, which fails on the read-only handle — exercising the
	// schema-exec error branch.
	path := filepath.Join(t.TempDir(), "mac.db")

	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("seed sql.Open() error = %v", err)
	}
	if err := seed.Ping(); err != nil { // creates the file on disk
		t.Fatalf("seed Ping() error = %v", err)
	}
	seed.Close()

	roDSN := "file:" + path + "?mode=ro"
	if _, err := Open(roDSN); err == nil {
		t.Fatal("Open(read-only) = nil error, want schema-exec error")
	}
}

func TestUpsertInstrument(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	insert := func(ins Instrument) {
		tx, err := database.Begin()
		if err != nil {
			t.Fatalf("Begin() error = %v", err)
		}
		if err := UpsertInstrument(tx, ins); err != nil {
			tx.Rollback()
			t.Fatalf("UpsertInstrument() error = %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
	}

	insert(Instrument{ID: "Kick", Name: "Kick v1", SamplePath: "/a/kick.wav", Config: `{"gain":1}`})
	insert(Instrument{ID: "Kick", Name: "Kick v2", SamplePath: "/b/kick.wav", Config: `{"gain":2}`})

	count, err := CountInstruments(database)
	if err != nil {
		t.Fatalf("CountInstruments() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CountInstruments() = %d, want 1 (upsert must not duplicate)", count)
	}

	list, err := ListInstruments(database)
	if err != nil {
		t.Fatalf("ListInstruments() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListInstruments() len = %d, want 1", len(list))
	}
	if list[0].Name != "Kick v2" || list[0].SamplePath != "/b/kick.wav" || list[0].Config != `{"gain":2}` {
		t.Fatalf("upsert did not overwrite fields: %+v", list[0])
	}
}

func TestListInstruments(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	// Insert out of order to prove ListInstruments sorts by ID.
	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	for _, ins := range []Instrument{
		{ID: "Snare", Name: "Snare", SamplePath: "/s.wav", Config: "{}"},
		{ID: "HiHat", Name: "HiHat", SamplePath: "/h.wav", Config: "{}"},
		{ID: "Kick", Name: "Kick", SamplePath: "/k.wav", Config: "{}"},
	} {
		if err := UpsertInstrument(tx, ins); err != nil {
			tx.Rollback()
			t.Fatalf("UpsertInstrument(%s) error = %v", ins.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	list, err := ListInstruments(database)
	if err != nil {
		t.Fatalf("ListInstruments() error = %v", err)
	}
	wantOrder := []string{"HiHat", "Kick", "Snare"}
	if len(list) != len(wantOrder) {
		t.Fatalf("ListInstruments() len = %d, want %d", len(list), len(wantOrder))
	}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Fatalf("ListInstruments()[%d].ID = %q, want %q", i, list[i].ID, want)
		}
	}
}

func TestUpsertInstrumentError(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	// Drop the table so the INSERT inside the transaction fails, exercising the
	// error branch.
	if _, err := database.Exec(`DROP TABLE instruments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer tx.Rollback()

	if err := UpsertInstrument(tx, Instrument{ID: "Kick", Name: "Kick", SamplePath: "/k.wav", Config: "{}"}); err == nil {
		t.Fatal("UpsertInstrument() = nil error, want error after table dropped")
	}
}

func TestListInstrumentsError(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	if _, err := database.Exec(`DROP TABLE instruments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	if _, err := ListInstruments(database); err == nil {
		t.Fatal("ListInstruments() = nil error, want error after table dropped")
	}
}

func TestOpenMigratesLegacySchema(t *testing.T) {
	// Simulate a pre-Spec-002 database: an instruments table without the
	// updated_at column. Opening it must add the column (guarded ALTER TABLE)
	// without error and leave the registry queryable.
	path := filepath.Join(t.TempDir(), "legacy.db")

	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("seed sql.Open() error = %v", err)
	}
	if _, err := seed.Exec(`CREATE TABLE instruments (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		sample_path TEXT NOT NULL,
		config      TEXT NOT NULL DEFAULT '{}',
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if _, err := seed.Exec(
		`INSERT INTO instruments (id, name, sample_path) VALUES ('Kick', 'Kick', '/k.wav')`,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	seed.Close()

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open(legacy) error = %v", err)
	}
	defer database.Close()

	// The updated_at column must now exist.
	var updatedAt string
	if err := database.QueryRow(
		`SELECT updated_at FROM instruments WHERE id = 'Kick'`,
	).Scan(&updatedAt); err != nil {
		t.Fatalf("querying updated_at after migration: %v", err)
	}

	// Re-opening must remain idempotent (column already present).
	database.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatalf("second Open(legacy) error = %v", err)
	}
	again.Close()
}

func TestSchemaCreatesInstrumentsTable(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	var name string
	err = database.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'instruments'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("querying for instruments table: %v", err)
	}
	if name != "instruments" {
		t.Fatalf("got table %q, want %q", name, "instruments")
	}
}
