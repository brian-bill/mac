package library

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchFreesoundEndToEnd exercises the freesound provider path: metadata
// resolution, token-authorized download, transcode, and catalog save.
func TestFetchFreesoundEndToEnd(t *testing.T) {
	body := wavFixtureBytes(t, []int{100, -100, 200})
	// The metadata response's download URL must point back at this same server,
	// so capture srv.URL after construction via an indirection.
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/sounds/555/"):
			w.Write([]byte(`{"id":555,"name":"clap","username":"carol",
			  "license":"http://creativecommons.org/licenses/by/4.0/",
			  "download":"` + baseURL + `/download/"}`))
		case strings.HasSuffix(r.URL.Path, "/download/"):
			if r.Header.Get("Authorization") == "" {
				http.Error(w, "no token", http.StatusUnauthorized)
				return
			}
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL
	freesoundAPIBase = srv.URL // client construction reads this
	out := t.TempDir()
	catPath := filepath.Join(out, "catalog.json")
	cat := &Catalog{
		CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Instruments: []Entry{{
			ID: "Clap", Name: "Clap", Category: "perc", Tier: TierDefault,
			Source: Source{Provider: ProviderFreesound, SoundID: 555, License: "CC-BY-4.0",
				Attribution: "carol (freesound.org/s/555)"},
		}},
	}
	rep, err := cat.Fetch(FetchOptions{
		OutRoot: out, UpdateHashes: true, CatalogPath: catPath,
		FreesoundToken: "secret", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Fetched) != 1 {
		t.Fatalf("fetched=%v, want 1", rep.Fetched)
	}
	// The catalog was saved with the filled hash.
	saved, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatalf("catalog not saved: %v", err)
	}
	var reloaded Catalog
	if err := json.Unmarshal(saved, &reloaded); err != nil {
		t.Fatal(err)
	}
	if reloaded.Instruments[0].SHA256 == "" {
		t.Fatal("saved catalog missing sha256")
	}
	// ATTRIBUTION.md credits the CC-BY sample.
	attr, _ := os.ReadFile(filepath.Join(out, "ATTRIBUTION.md"))
	if !strings.Contains(string(attr), "carol") {
		t.Fatalf("attribution missing carol: %s", attr)
	}
}

// TestFetchFreesoundNCSkipped: a Freesound sound that resolves to NC is skipped
// with a warning rather than aborting the run.
func TestFetchFreesoundNCSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":9,"name":"x","username":"z",
		  "license":"http://creativecommons.org/licenses/by-nc/3.0/","download":"http://x/9"}`))
	}))
	defer srv.Close()
	freesoundAPIBase = srv.URL

	out := t.TempDir()
	cat := &Catalog{
		CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Instruments: []Entry{{
			ID: "X", Name: "X", Category: "perc", Tier: TierDefault,
			Source: Source{Provider: ProviderFreesound, SoundID: 9, License: "CC-BY-4.0"},
		}},
	}
	rep, err := cat.Fetch(FetchOptions{OutRoot: out, UpdateHashes: true, FreesoundToken: "t", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("NC sound should skip, not error: %v", err)
	}
	if len(rep.Skipped) != 1 || len(rep.Warnings) == 0 {
		t.Fatalf("skipped=%v warnings=%v, want 1 skip with warning", rep.Skipped, rep.Warnings)
	}
}

func TestFloatToInt16Clipping(t *testing.T) {
	tests := []struct {
		in   float64
		want int16
	}{
		{0, 0},
		{2.0, 32767},   // above full scale clips high
		{-2.0, -32768}, // below clips low
		{0.5, 16384},
	}
	for _, tc := range tests {
		if got := floatToInt16(tc.in); got != tc.want {
			t.Errorf("floatToInt16(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestWriteSeekBufferSeek(t *testing.T) {
	w := newWriteSeekBuffer()
	w.Write([]byte("hello"))
	if _, err := w.Seek(0, 0); err != nil { // start
		t.Fatal(err)
	}
	w.Write([]byte("H"))
	if _, err := w.Seek(0, 2); err != nil { // end
		t.Fatal(err)
	}
	if _, err := w.Seek(-1, 1); err != nil { // current
		t.Fatal(err)
	}
	if _, err := w.Seek(-100, 0); err == nil {
		t.Fatal("expected negative-seek error")
	}
	if _, err := w.Seek(0, 99); err == nil {
		t.Fatal("expected bad-whence error")
	}
	if string(w.Bytes()) != "Hello" {
		t.Fatalf("buffer = %q, want Hello", w.Bytes())
	}
}

func TestTranscodeErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := Transcode("/nonexistent.wav", TranscodeOptions{}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("not a wav", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "x.wav")
		os.WriteFile(p, []byte("not a wav"), 0o644)
		if _, err := Transcode(p, TranscodeOptions{}); err == nil {
			t.Fatal("expected invalid-wav error")
		}
	})
}

func TestCatalogSave(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	cat := &Catalog{
		CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Instruments:     []Entry{validEntry()},
	}
	if err := cat.save(p); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadCatalog(p)
	if err != nil {
		t.Fatalf("reload saved catalog: %v", err)
	}
	if len(reloaded.Instruments) != 1 {
		t.Fatalf("reloaded %d instruments, want 1", len(reloaded.Instruments))
	}
}
