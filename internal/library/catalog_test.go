package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validEntry returns a minimal valid github default-tier entry for mutation in
// table-driven cases.
func validEntry() Entry {
	return Entry{
		ID:       "Kick",
		Name:     "Kick",
		Category: "drums/acoustic",
		Tier:     TierDefault,
		Source: Source{
			Provider: ProviderGitHub,
			URL:      "https://example.com/kick.wav",
			License:  "CC0-1.0",
		},
	}
}

func TestEntryValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Entry)
		wantErr string
	}{
		{"valid", func(*Entry) {}, ""},
		{"bad id", func(e *Entry) { e.ID = "9bad" }, "invalid id"},
		{"empty id", func(e *Entry) { e.ID = "" }, "invalid id"},
		{"missing name", func(e *Entry) { e.Name = "  " }, "missing name"},
		{"missing tier", func(e *Entry) { e.Tier = "" }, "missing tier"},
		{"unknown tier", func(e *Entry) { e.Tier = "premium" }, "unknown tier"},
		{"missing provider", func(e *Entry) { e.Source.Provider = "" }, "missing source.provider"},
		{"unknown provider", func(e *Entry) { e.Source.Provider = "ftp" }, "unknown source.provider"},
		{"empty license", func(e *Entry) { e.Source.License = "" }, "empty source.license"},
		{"github without url", func(e *Entry) { e.Source.URL = "" }, "needs a url"},
		{"freesound without id", func(e *Entry) {
			e.Source.Provider = ProviderFreesound
			e.Source.URL = ""
			e.Source.SoundID = 0
		}, "needs a positive sound_id"},
		{"http must be restricted", func(e *Entry) {
			e.Source.Provider = ProviderHTTP
		}, "must be tier"},
		{"category traversal", func(e *Entry) { e.Category = "../etc" }, "escapes the output root"},
		{"absolute category", func(e *Entry) { e.Category = "/abs" }, "must be relative"},
		{"empty category", func(e *Entry) { e.Category = "" }, "missing category"},
		{"bad config json", func(e *Entry) { e.Config = json.RawMessage(`{bad`) }, "not valid JSON"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := validEntry()
			tc.mutate(&e)
			err := e.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCatalogValidate(t *testing.T) {
	base := func() *Catalog {
		return &Catalog{
			CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
			Instruments:     []Entry{validEntry()},
		}
	}

	t.Run("valid", func(t *testing.T) {
		if err := base().Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong canonical format", func(t *testing.T) {
		c := base()
		c.CanonicalFormat.SampleRate = 48000
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "canonical_format") {
			t.Fatalf("got %v, want canonical_format error", err)
		}
	})

	t.Run("empty instruments", func(t *testing.T) {
		c := base()
		c.Instruments = nil
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "no instruments") {
			t.Fatalf("got %v, want no-instruments error", err)
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		c := base()
		c.Instruments = append(c.Instruments, validEntry())
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate instrument id") {
			t.Fatalf("got %v, want duplicate-id error", err)
		}
	})
}

func TestLoadCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	good := `{
      "canonical_format": {"sample_rate":44100,"channels":1,"bit_depth":16},
      "instruments": [
        {"id":"Kick","name":"Kick","category":"drums","tier":"default",
         "source":{"provider":"github","url":"https://x/k.wav","license":"CC0-1.0"}}
      ]
    }`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.Instruments) != 1 || cat.Instruments[0].ID != "Kick" {
		t.Fatalf("unexpected catalog: %+v", cat)
	}

	t.Run("missing file", func(t *testing.T) {
		if _, err := LoadCatalog(filepath.Join(dir, "nope.json")); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		os.WriteFile(p, []byte("{not json"), 0o644)
		if _, err := LoadCatalog(p); err == nil {
			t.Fatal("expected parse error")
		}
	})
}
