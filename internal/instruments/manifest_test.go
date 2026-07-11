package instruments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeInstrument creates a manifest.json (and optionally a sample file) inside
// a fresh directory, returning that directory. sampleName is created as an
// empty file when non-empty, letting tests control whether the declared sample
// exists.
func writeInstrument(t *testing.T, manifestJSON, sampleName string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifestFileName), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if sampleName != "" {
		if err := os.WriteFile(filepath.Join(dir, sampleName), []byte("wav"), 0o644); err != nil {
			t.Fatalf("write sample: %v", err)
		}
	}
	return dir
}

func TestParseManifest(t *testing.T) {
	t.Run("valid manifest decodes", func(t *testing.T) {
		dir := writeInstrument(t, `{"id":"Kick","name":"Kick","sample":"kick.wav"}`, "kick.wav")
		m, err := ParseManifest(filepath.Join(dir, manifestFileName))
		if err != nil {
			t.Fatalf("ParseManifest() error = %v", err)
		}
		if m.ID != "Kick" || m.Name != "Kick" || m.Sample != "kick.wav" {
			t.Fatalf("ParseManifest() = %+v, unexpected fields", m)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		_, err := ParseManifest(filepath.Join(t.TempDir(), "nope.json"))
		if err == nil {
			t.Fatal("ParseManifest() = nil error, want error for missing file")
		}
		if !strings.Contains(err.Error(), "read manifest") {
			t.Fatalf("error %q missing 'read manifest'", err.Error())
		}
	})

	t.Run("malformed JSON errors", func(t *testing.T) {
		dir := writeInstrument(t, `{"id": "Kick",`, "")
		_, err := ParseManifest(filepath.Join(dir, manifestFileName))
		if err == nil {
			t.Fatal("ParseManifest() = nil error, want error for malformed JSON")
		}
		if !strings.Contains(err.Error(), "parse manifest") {
			t.Fatalf("error %q missing 'parse manifest'", err.Error())
		}
	})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name         string
		manifestJSON string
		sampleName   string // sample file to create ("" = none)
		wantErr      bool
		errContains  string
	}{
		{
			name:         "valid",
			manifestJSON: `{"id":"Kick","name":"Kick Drum","sample":"kick.wav","config":{"gain":1}}`,
			sampleName:   "kick.wav",
		},
		{
			name:         "absent config defaults",
			manifestJSON: `{"id":"Snare","name":"Snare","sample":"snare.wav"}`,
			sampleName:   "snare.wav",
		},
		{
			name:         "missing id",
			manifestJSON: `{"name":"Kick","sample":"kick.wav"}`,
			sampleName:   "kick.wav",
			wantErr:      true,
			errContains:  "invalid id",
		},
		{
			name:         "id with space",
			manifestJSON: `{"id":"Big Kick","name":"Kick","sample":"kick.wav"}`,
			sampleName:   "kick.wav",
			wantErr:      true,
			errContains:  "invalid id",
		},
		{
			name:         "id with leading digit",
			manifestJSON: `{"id":"808","name":"Kick","sample":"kick.wav"}`,
			sampleName:   "kick.wav",
			wantErr:      true,
			errContains:  "invalid id",
		},
		{
			name:         "id with bracket",
			manifestJSON: `{"id":"[Kick]","name":"Kick","sample":"kick.wav"}`,
			sampleName:   "kick.wav",
			wantErr:      true,
			errContains:  "invalid id",
		},
		{
			name:         "missing name",
			manifestJSON: `{"id":"Kick","name":"   ","sample":"kick.wav"}`,
			sampleName:   "kick.wav",
			wantErr:      true,
			errContains:  "missing name",
		},
		{
			name:         "missing sample field",
			manifestJSON: `{"id":"Kick","name":"Kick"}`,
			sampleName:   "kick.wav",
			wantErr:      true,
			errContains:  "missing sample",
		},
		{
			name:         "sample file does not exist",
			manifestJSON: `{"id":"Kick","name":"Kick","sample":"kick.wav"}`,
			sampleName:   "", // not created
			wantErr:      true,
			errContains:  "does not exist",
		},
		{
			name:         "sample wrong extension",
			manifestJSON: `{"id":"Kick","name":"Kick","sample":"kick.mp3"}`,
			sampleName:   "kick.mp3",
			wantErr:      true,
			errContains:  "unsupported sample extension",
		},
		{
			name:         "sample traversal escapes folder",
			manifestJSON: `{"id":"Kick","name":"Kick","sample":"../evil.wav"}`,
			sampleName:   "",
			wantErr:      true,
			errContains:  "escapes its instrument folder",
		},
		{
			name:         "invalid config JSON",
			manifestJSON: `{"id":"Kick","name":"Kick","sample":"kick.wav","config":"not-an-object-but-valid"}`,
			sampleName:   "kick.wav",
			// A JSON string is valid JSON, so this should NOT error; see note below.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeInstrument(t, tt.manifestJSON, tt.sampleName)
			m, err := ParseManifest(filepath.Join(dir, manifestFileName))
			if err != nil {
				t.Fatalf("ParseManifest() error = %v", err)
			}

			samplePath, configJSON, err := m.validate(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validate() = nil error, want error")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate() error = %v, want nil", err)
			}
			if !filepath.IsAbs(samplePath) {
				t.Fatalf("validate() samplePath = %q, want absolute", samplePath)
			}
			if configJSON == "" {
				t.Fatal("validate() configJSON is empty, want at least '{}'")
			}
		})
	}
}

func TestValidateConfigDefaultsToEmptyObject(t *testing.T) {
	dir := writeInstrument(t, `{"id":"Kick","name":"Kick","sample":"kick.wav"}`, "kick.wav")
	m, err := ParseManifest(filepath.Join(dir, manifestFileName))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	_, configJSON, err := m.validate(dir)
	if err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	if configJSON != "{}" {
		t.Fatalf("configJSON = %q, want %q", configJSON, "{}")
	}
}

func TestNormalizeConfigInvalidJSON(t *testing.T) {
	// json.RawMessage that is syntactically invalid must be rejected.
	_, err := normalizeConfig([]byte(`{bad`))
	if err == nil {
		t.Fatal("normalizeConfig() = nil error, want error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error %q missing 'not valid JSON'", err.Error())
	}
}

func TestNormalizeConfigCompacts(t *testing.T) {
	got, err := normalizeConfig([]byte("{\n  \"gain\": 1.0\n}"))
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if strings.ContainsAny(got, "\n ") {
		t.Fatalf("normalizeConfig() = %q, want compact form", got)
	}
}
