package instruments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bryanbill/mac/internal/db"
)

// mkInstrument creates an instrument folder (manifest + sample) under parent.
func mkInstrument(t *testing.T, parent, folder, manifestJSON, sampleName string) {
	t.Helper()
	dir := filepath.Join(parent, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFileName), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if sampleName != "" {
		if err := os.WriteFile(filepath.Join(dir, sampleName), []byte("wav"), 0o644); err != nil {
			t.Fatalf("write sample: %v", err)
		}
	}
}

func TestScan_HappyPath(t *testing.T) {
	root := filepath.Join("testdata", "library")

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	// Two instruments: Kick (top level) and Snare (nested under percussion/).
	if len(got) != 2 {
		t.Fatalf("Scan() returned %d instruments, want 2: %+v", len(got), got)
	}

	// Sorted by ID: Kick, Snare.
	if got[0].ID != "Kick" || got[1].ID != "Snare" {
		t.Fatalf("Scan() IDs = [%s %s], want [Kick Snare]", got[0].ID, got[1].ID)
	}

	for _, ins := range got {
		if !filepath.IsAbs(ins.SamplePath) {
			t.Errorf("instrument %s sample_path %q not absolute", ins.ID, ins.SamplePath)
		}
		if !strings.HasSuffix(ins.SamplePath, ".wav") {
			t.Errorf("instrument %s sample_path %q not a .wav", ins.ID, ins.SamplePath)
		}
	}

	// Snare has no config block → defaults to "{}"; Kick declares one.
	if got[1].Config != "{}" {
		t.Errorf("Snare config = %q, want %q", got[1].Config, "{}")
	}
	if got[0].Config == "{}" {
		t.Errorf("Kick config = %q, want a populated object", got[0].Config)
	}
}

func TestScan_IgnoresFoldersWithoutManifest(t *testing.T) {
	// The fixture library contains loose-samples/orphan.wav with no manifest;
	// the happy-path count of 2 already proves it is ignored, but assert here
	// against a purpose-built root too.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty", "loose.wav"), []byte("wav"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Scan() = %d instruments, want 0", len(got))
	}
}

func TestScan_DuplicateID(t *testing.T) {
	root := t.TempDir()
	mkInstrument(t, root, "a", `{"id":"Kick","name":"Kick A","sample":"k.wav"}`, "k.wav")
	mkInstrument(t, root, "b", `{"id":"Kick","name":"Kick B","sample":"k.wav"}`, "k.wav")

	_, err := Scan(root)
	if err == nil {
		t.Fatal("Scan() = nil error, want duplicate id error")
	}
	if !strings.Contains(err.Error(), "duplicate instrument id") {
		t.Fatalf("error %q missing 'duplicate instrument id'", err.Error())
	}
	// Both manifest paths should be named.
	if !strings.Contains(err.Error(), filepath.Join("a", manifestFileName)) ||
		!strings.Contains(err.Error(), filepath.Join("b", manifestFileName)) {
		t.Fatalf("error %q does not name both manifest paths", err.Error())
	}
}

func TestScan_FailFast(t *testing.T) {
	root := t.TempDir()
	mkInstrument(t, root, "good", `{"id":"Kick","name":"Kick","sample":"k.wav"}`, "k.wav")
	mkInstrument(t, root, "bad", `{"id":"Snare","name":"Snare","sample":"missing.wav"}`, "") // no sample

	_, err := Scan(root)
	if err == nil {
		t.Fatal("Scan() = nil error, want fail-fast error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error %q missing sample-does-not-exist detail", err.Error())
	}
}

func TestScan_RootMissing(t *testing.T) {
	_, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("Scan() = nil error, want error for missing root")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error %q missing 'does not exist'", err.Error())
	}
}

func TestScan_RootIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Scan(file)
	if err == nil {
		t.Fatal("Scan() = nil error, want error for file root")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error %q missing 'not a directory'", err.Error())
	}
}

func TestRegister_Idempotent(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	defer database.Close()

	root := filepath.Join("testdata", "library")

	n1, err := Register(database, root)
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if n1 != 2 {
		t.Fatalf("first Register() = %d, want 2", n1)
	}

	count1, err := db.CountInstruments(database)
	if err != nil {
		t.Fatalf("CountInstruments() error = %v", err)
	}

	n2, err := Register(database, root)
	if err != nil {
		t.Fatalf("second Register() error = %v", err)
	}
	if n2 != 2 {
		t.Fatalf("second Register() = %d, want 2", n2)
	}

	count2, err := db.CountInstruments(database)
	if err != nil {
		t.Fatalf("CountInstruments() error = %v", err)
	}
	if count1 != count2 {
		t.Fatalf("row count changed across re-registration: %d -> %d", count1, count2)
	}
	if count2 != 2 {
		t.Fatalf("CountInstruments() = %d, want 2 (idempotent)", count2)
	}

	// Verify list is sorted and fields round-tripped.
	list, err := db.ListInstruments(database)
	if err != nil {
		t.Fatalf("ListInstruments() error = %v", err)
	}
	if len(list) != 2 || list[0].ID != "Kick" || list[1].ID != "Snare" {
		t.Fatalf("ListInstruments() = %+v, want [Kick Snare]", list)
	}
}

func TestRegister_Atomic(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "mac.db"))
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	defer database.Close()

	// A root whose second instrument is broken: Scan fails fast before any DB
	// write, so the registry must remain empty.
	root := t.TempDir()
	mkInstrument(t, root, "good", `{"id":"Kick","name":"Kick","sample":"k.wav"}`, "k.wav")
	mkInstrument(t, root, "zbad", `{"id":"Snare","name":"Snare","sample":"missing.wav"}`, "")

	if _, err := Register(database, root); err == nil {
		t.Fatal("Register() = nil error, want error from broken manifest")
	}

	count, err := db.CountInstruments(database)
	if err != nil {
		t.Fatalf("CountInstruments() error = %v", err)
	}
	if count != 0 {
		t.Fatalf("CountInstruments() = %d after failed Register, want 0 (atomic)", count)
	}
}
