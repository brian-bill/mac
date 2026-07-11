package library

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// wavFixtureBytes builds a small mono 16-bit 44100 WAV in memory.
func wavFixtureBytes(t *testing.T, samples []int) []byte {
	t.Helper()
	ws := newWriteSeekBuffer()
	enc := wav.NewEncoder(ws, 44100, 16, 1, wavFormatPCM)
	ib := &audio.IntBuffer{
		Format:         &audio.Format{SampleRate: 44100, NumChannels: 1},
		Data:           samples,
		SourceBitDepth: 16,
	}
	if err := enc.Write(ib); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return ws.Bytes()
}

// stubServer serves a fixed WAV at any /*.wav path.
func stubServer(t *testing.T, body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing.wav") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(body)
	}))
}

func githubCatalog(url string) *Catalog {
	return &Catalog{
		CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Instruments: []Entry{{
			ID:       "Kick",
			Name:     "Kick",
			Category: "drums",
			Tier:     TierDefault,
			Source:   Source{Provider: ProviderGitHub, URL: url, License: "CC0-1.0"},
		}},
	}
}

func TestFetchWritesInstrument(t *testing.T) {
	body := wavFixtureBytes(t, []int{1000, -1000, 2000, -2000})
	srv := stubServer(t, body)
	defer srv.Close()

	out := t.TempDir()
	cat := githubCatalog(srv.URL + "/kick.wav")
	rep, err := cat.Fetch(FetchOptions{
		OutRoot:      out,
		UpdateHashes: true,
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Fetched) != 1 || rep.Fetched[0] != "Kick" {
		t.Fatalf("report fetched = %v, want [Kick]", rep.Fetched)
	}
	if len(rep.Updated) != 1 {
		t.Fatalf("report updated = %v, want 1 (hash filled)", rep.Updated)
	}

	wavPath := filepath.Join(out, "drums", "kick", "Kick.wav")
	if _, err := os.Stat(wavPath); err != nil {
		t.Fatalf("expected vendored WAV at %s: %v", wavPath, err)
	}
	manifestPath := filepath.Join(out, "drums", "kick", "manifest.json")
	m, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if !strings.Contains(string(m), `"id": "Kick"`) || !strings.Contains(string(m), `"sample": "Kick.wav"`) {
		t.Fatalf("manifest content unexpected: %s", m)
	}
	if _, err := os.Stat(filepath.Join(out, "ATTRIBUTION.md")); err != nil {
		t.Fatalf("expected ATTRIBUTION.md: %v", err)
	}
	if cat.Instruments[0].SHA256 == "" {
		t.Fatal("sha256 was not filled in")
	}
}

func TestFetchIdempotentAndVerify(t *testing.T) {
	body := wavFixtureBytes(t, []int{500, -500})
	srv := stubServer(t, body)
	defer srv.Close()
	out := t.TempDir()
	cat := githubCatalog(srv.URL + "/kick.wav")

	// First run seeds the hash and vendors the file.
	if _, err := cat.Fetch(FetchOptions{OutRoot: out, UpdateHashes: true, HTTPClient: srv.Client()}); err != nil {
		t.Fatal(err)
	}
	// Second run: hash present + file present → verified, not re-fetched.
	rep, err := cat.Fetch(FetchOptions{OutRoot: out, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Fetched) != 0 || len(rep.Verified) != 1 {
		t.Fatalf("second run fetched=%v verified=%v, want 0/1", rep.Fetched, rep.Verified)
	}

	// Offline-verify passes on the good library.
	if _, err := cat.Fetch(FetchOptions{OutRoot: out, OfflineVerify: true}); err != nil {
		t.Fatalf("offline-verify should pass: %v", err)
	}

	// Tamper with the WAV → offline-verify fails (determinism gate).
	wavPath := filepath.Join(out, "drums", "kick", "Kick.wav")
	if err := os.WriteFile(wavPath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Fetch(FetchOptions{OutRoot: out, OfflineVerify: true}); err == nil {
		t.Fatal("offline-verify should fail on tampered WAV")
	}
}

func TestFetchHashMismatch(t *testing.T) {
	body := wavFixtureBytes(t, []int{1, 2, 3})
	srv := stubServer(t, body)
	defer srv.Close()
	out := t.TempDir()
	cat := githubCatalog(srv.URL + "/kick.wav")
	cat.Instruments[0].SHA256 = "deadbeef" // wrong hash, no local file yet

	_, err := cat.Fetch(FetchOptions{OutRoot: out, HTTPClient: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("got %v, want sha256 mismatch", err)
	}
}

func TestFetchMissingHashNoUpdate(t *testing.T) {
	body := wavFixtureBytes(t, []int{1, 2, 3})
	srv := stubServer(t, body)
	defer srv.Close()
	out := t.TempDir()
	cat := githubCatalog(srv.URL + "/kick.wav") // empty hash, UpdateHashes off

	_, err := cat.Fetch(FetchOptions{OutRoot: out, HTTPClient: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "no sha256") {
		t.Fatalf("got %v, want no-sha256 error", err)
	}
}

func TestFetchRestrictedSkippedByDefault(t *testing.T) {
	body := wavFixtureBytes(t, []int{1, 2})
	srv := stubServer(t, body)
	defer srv.Close()
	out := t.TempDir()
	cat := &Catalog{
		CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Instruments: []Entry{{
			ID: "Loop", Name: "Loop", Category: "loops", Tier: TierRestricted,
			Source: Source{Provider: ProviderHTTP, URL: srv.URL + "/loop.wav", License: "Proprietary"},
		}},
	}
	rep, err := cat.Fetch(FetchOptions{OutRoot: out, UpdateHashes: true, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 1 || len(rep.Fetched) != 0 {
		t.Fatalf("restricted default: skipped=%v fetched=%v, want 1/0", rep.Skipped, rep.Fetched)
	}
	// The restricted dir must not appear in the committed tree.
	if _, err := os.Stat(filepath.Join(out, "loops")); !os.IsNotExist(err) {
		t.Fatal("restricted entry should not be written when --allow-restricted is off")
	}

	// With allow-restricted, it is fetched into the git-ignored _restricted dir.
	rep2, err := cat.Fetch(FetchOptions{OutRoot: out, AllowRestricted: true, UpdateHashes: true, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Fetched) != 1 {
		t.Fatalf("allow-restricted fetched=%v, want 1", rep2.Fetched)
	}
	wavPath := filepath.Join(out, restrictedDirName, "loops", "loop", "Loop.wav")
	if _, err := os.Stat(wavPath); err != nil {
		t.Fatalf("expected restricted WAV at %s: %v", wavPath, err)
	}
}

func TestFetchOnlyFilter(t *testing.T) {
	body := wavFixtureBytes(t, []int{1, 2})
	srv := stubServer(t, body)
	defer srv.Close()
	out := t.TempDir()
	cat := &Catalog{
		CanonicalFormat: CanonicalFormat{SampleRate: 44100, Channels: 1, BitDepth: 16},
		Instruments: []Entry{
			{ID: "Kick", Name: "Kick", Category: "d", Tier: TierDefault,
				Source: Source{Provider: ProviderGitHub, URL: srv.URL + "/k.wav", License: "CC0-1.0"}},
			{ID: "Snare", Name: "Snare", Category: "d", Tier: TierDefault,
				Source: Source{Provider: ProviderGitHub, URL: srv.URL + "/s.wav", License: "CC0-1.0"}},
		},
	}
	rep, err := cat.Fetch(FetchOptions{
		OutRoot: out, UpdateHashes: true, HTTPClient: srv.Client(),
		Only: map[string]struct{}{"Snare": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Fetched) != 1 || rep.Fetched[0] != "Snare" {
		t.Fatalf("only-filter fetched=%v, want [Snare]", rep.Fetched)
	}
}

func TestFetchDownloadError(t *testing.T) {
	body := wavFixtureBytes(t, []int{1})
	srv := stubServer(t, body)
	defer srv.Close()
	out := t.TempDir()
	cat := githubCatalog(srv.URL + "/missing.wav") // 404

	_, err := cat.Fetch(FetchOptions{OutRoot: out, UpdateHashes: true, HTTPClient: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("got %v, want HTTP 404", err)
	}
}
