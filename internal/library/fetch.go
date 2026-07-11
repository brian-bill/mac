package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// restrictedDirName is the git-ignored subdirectory under the output root into
// which restricted-tier samples are fetched. They are never committed.
const restrictedDirName = "_restricted"

// FetchOptions configures a fetch run.
type FetchOptions struct {
	// OutRoot is the instruments/ directory into which folders are written.
	OutRoot string
	// AllowRestricted enables restricted-tier entries (fetched into OutRoot/_restricted).
	AllowRestricted bool
	// UpdateHashes fills in absent sha256 fields (and rewrites CatalogPath) instead
	// of failing on a missing hash. Present hashes are still verified.
	UpdateHashes bool
	// OfflineVerify skips all downloading and only re-hashes already-vendored WAVs
	// against the catalog, failing on drift. The CI determinism gate (GOAL.md §4).
	OfflineVerify bool
	// Only, when non-empty, restricts the run to entries whose ID is in the set.
	Only map[string]struct{}
	// CatalogPath is where UpdateHashes writes the seeded catalog back.
	CatalogPath string
	// FreesoundToken authorizes Freesound downloads (from FREESOUND_API_TOKEN).
	FreesoundToken string
	// HTTPClient overrides the default client (tests inject httptest).
	HTTPClient *http.Client
	// Out receives human-readable progress; nil discards it.
	Out io.Writer
}

// Report summarizes a fetch run.
type Report struct {
	Fetched  []string // entries downloaded and written this run
	Verified []string // entries already present whose hash matched
	Skipped  []string // entries skipped (restricted-off, freesound NC, --only filter)
	Updated  []string // entries whose sha256 was filled in (UpdateHashes)
	Warnings []string // non-fatal issues (e.g. skipped-with-reason)
}

// Fetch executes the catalog against opts: for each in-scope entry it ensures a
// canonical WAV exists at the entry's destination, downloading and transcoding
// when necessary, verifying the SHA-256, then writes the instrument's
// manifest.json. Finally it (re)generates ATTRIBUTION.md.
//
// Fetch is idempotent: an already-vendored, hash-matching WAV is verified and
// left untouched. It never leaves a partial WAV behind (writes go to a temp file
// then rename).
func (cat *Catalog) Fetch(opts FetchOptions) (*Report, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	fs := newFreesoundClient(opts.FreesoundToken, opts.HTTPClient)

	report := &Report{}
	var attributions []string
	catalogDirty := false

	// Entries are processed in ID order for deterministic output/reporting.
	entries := make([]*Entry, len(cat.Instruments))
	for i := range cat.Instruments {
		entries[i] = &cat.Instruments[i]
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	for _, e := range entries {
		if len(opts.Only) > 0 {
			if _, ok := opts.Only[e.ID]; !ok {
				continue
			}
		}
		if e.Tier == TierRestricted && !opts.AllowRestricted {
			report.Skipped = append(report.Skipped, e.ID)
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%s: restricted tier skipped (use --allow-restricted)", e.ID))
			continue
		}

		destDir := cat.destDir(e, opts.OutRoot)
		wavPath := filepath.Join(destDir, e.ID+".wav")

		if e.Source.Attribution != "" {
			attributions = append(attributions, fmt.Sprintf("- %s — %s (%s)", e.ID, e.Source.Attribution, e.Source.License))
		}

		// Offline verify: hash the committed WAV, never touch the network.
		if opts.OfflineVerify {
			if err := verifyExisting(e, wavPath); err != nil {
				return nil, err
			}
			report.Verified = append(report.Verified, e.ID)
			continue
		}

		// If a hash-matching WAV already exists, skip the download (idempotent).
		if e.SHA256 != "" {
			if ok, _ := hashMatches(wavPath, e.SHA256); ok {
				if err := writeManifest(e, destDir, wavPath); err != nil {
					return nil, err
				}
				report.Verified = append(report.Verified, e.ID)
				fmt.Fprintf(opts.Out, "  ok    %s (cached)\n", e.ID)
				continue
			}
		}

		// Download → transcode → canonical WAV bytes.
		wavBytes, resolvedLicense, resolvedAttr, err := fetchEntry(e, fs, opts)
		if err != nil {
			if skip, reason := isSkippable(err); skip {
				report.Skipped = append(report.Skipped, e.ID)
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", e.ID, reason))
				fmt.Fprintf(opts.Out, "  skip  %s (%s)\n", e.ID, reason)
				continue
			}
			return nil, fmt.Errorf("fetch %s: %w", e.ID, err)
		}

		sum := sha256Hex(wavBytes)
		switch {
		case e.SHA256 == "" && opts.UpdateHashes:
			e.SHA256 = sum
			catalogDirty = true
			report.Updated = append(report.Updated, e.ID)
		case e.SHA256 == "":
			return nil, fmt.Errorf(
				"fetch %s: catalog entry has no sha256; re-run with --update-hashes to seed it (computed %s)",
				e.ID, sum)
		case e.SHA256 != sum:
			return nil, fmt.Errorf(
				"fetch %s: sha256 mismatch — catalog says %s but transcoded output is %s (source drift?)",
				e.ID, e.SHA256, sum)
		}

		// Record any license/attribution the provider resolved (Freesound).
		if resolvedLicense != "" && e.Source.License == "" {
			e.Source.License = resolvedLicense
			catalogDirty = true
		}
		if resolvedAttr != "" {
			attributions = append(attributions, fmt.Sprintf("- %s — %s (%s)", e.ID, resolvedAttr, e.Source.License))
		}

		if err := writeWAVAtomic(wavPath, wavBytes); err != nil {
			return nil, fmt.Errorf("fetch %s: %w", e.ID, err)
		}
		if err := writeManifest(e, destDir, wavPath); err != nil {
			return nil, err
		}
		report.Fetched = append(report.Fetched, e.ID)
		fmt.Fprintf(opts.Out, "  get   %s\n", e.ID)
	}

	// Regenerate attribution and optionally the seeded catalog.
	if !opts.OfflineVerify {
		if err := writeAttribution(opts.OutRoot, attributions); err != nil {
			return nil, err
		}
	}
	if catalogDirty && opts.UpdateHashes && opts.CatalogPath != "" {
		if err := cat.save(opts.CatalogPath); err != nil {
			return nil, err
		}
	}
	return report, nil
}

// destDir returns the on-disk instrument folder for an entry, routing restricted
// entries under the git-ignored _restricted dir.
func (cat *Catalog) destDir(e *Entry, outRoot string) string {
	base := outRoot
	if e.Tier == TierRestricted {
		base = filepath.Join(outRoot, restrictedDirName)
	}
	return filepath.Join(base, filepath.FromSlash(e.Category), strings.ToLower(e.ID))
}

// fetchEntry downloads the raw sample for e and transcodes it to canonical WAV
// bytes. Returns any provider-resolved license/attribution (Freesound).
func fetchEntry(e *Entry, fs *freesoundClient, opts FetchOptions) (wav []byte, license, attribution string, err error) {
	var srcURL string
	switch e.Source.Provider {
	case ProviderGitHub, ProviderHTTP:
		srcURL = e.Source.URL
	case ProviderFreesound:
		u, lic, attr, e2 := fs.resolveDownloadURL(e.Source.SoundID)
		if e2 != nil {
			return nil, "", "", e2
		}
		srcURL, license, attribution = u, lic, attr
	default:
		return nil, "", "", fmt.Errorf("unknown provider %q", e.Source.Provider)
	}

	raw, err := downloadToTemp(srcURL, e, fs, opts)
	if err != nil {
		return nil, "", "", err
	}
	defer os.Remove(raw)

	out, err := Transcode(raw, e.Transform.options())
	if err != nil {
		return nil, "", "", err
	}
	return out, license, attribution, nil
}

// downloadToTemp streams srcURL to a temp file and returns its path. Freesound
// original URLs are token-authorized; other providers use a plain GET.
func downloadToTemp(srcURL string, e *Entry, fs *freesoundClient, opts FetchOptions) (string, error) {
	var resp *http.Response
	var err error
	if e.Source.Provider == ProviderFreesound {
		resp, err = fs.authorizedGet(srcURL)
	} else {
		resp, err = opts.HTTPClient.Get(srcURL)
	}
	if err != nil {
		return "", fmt.Errorf("download %s: %w", srcURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", srcURL, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "mac-fetch-*.wav")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	n, err := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download %s: %w", srcURL, err)
	}
	if closeErr != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download %s: close temp: %w", srcURL, closeErr)
	}
	if n == 0 {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download %s: empty response body", srcURL)
	}
	return tmp.Name(), nil
}

// writeManifest writes the instrument's manifest.json next to its WAV, matching
// the Spec 002 schema (id, name, sample, config).
func writeManifest(e *Entry, destDir, wavPath string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create instrument dir %s: %w", destDir, err)
	}
	type manifest struct {
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		Sample string          `json:"sample"`
		Config json.RawMessage `json:"config,omitempty"`
	}
	m := manifest{ID: e.ID, Name: e.Name, Sample: filepath.Base(wavPath), Config: e.Config}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest for %s: %w", e.ID, err)
	}
	data = append(data, '\n')
	path := filepath.Join(destDir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	return nil
}

// verifyExisting hashes an already-vendored WAV against the entry's recorded
// sha256, erroring on absence or drift (offline-verify path).
func verifyExisting(e *Entry, wavPath string) error {
	if e.SHA256 == "" {
		return fmt.Errorf("offline-verify %s: entry has no sha256 to verify against", e.ID)
	}
	ok, err := hashMatches(wavPath, e.SHA256)
	if err != nil {
		return fmt.Errorf("offline-verify %s: %w", e.ID, err)
	}
	if !ok {
		return fmt.Errorf("offline-verify %s: %s does not match catalog sha256 %s", e.ID, wavPath, e.SHA256)
	}
	return nil
}

// writeWAVAtomic writes bytes to path via a temp file + rename so a crash never
// leaves a partial WAV.
func writeWAVAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.wav")
	if err != nil {
		return fmt.Errorf("create temp wav: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp wav: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp wav: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename wav into place: %w", err)
	}
	return nil
}

// writeAttribution regenerates ATTRIBUTION.md crediting every CC-BY sample.
func writeAttribution(outRoot string, lines []string) error {
	sort.Strings(lines)
	var b strings.Builder
	b.WriteString("# Attribution\n\n")
	b.WriteString("Samples in this library that require attribution are credited below.\n")
	b.WriteString("CC0 / public-domain samples need no attribution and are not listed.\n\n")
	if len(lines) == 0 {
		b.WriteString("_No attribution-required samples are currently vendored._\n")
	} else {
		for _, l := range lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	path := filepath.Join(outRoot, "ATTRIBUTION.md")
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return fmt.Errorf("create output root %s: %w", outRoot, err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write attribution %s: %w", path, err)
	}
	return nil
}

// save writes the catalog back to path (used by --update-hashes), preserving a
// stable, human-readable, diff-friendly form.
func (cat *Catalog) save(path string) error {
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write catalog %s: %w", path, err)
	}
	return nil
}

// hashMatches reports whether the file at path hashes to want.
func hashMatches(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

// sha256Hex returns the hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// skippableError marks a fetch failure that should skip the entry with a warning
// rather than abort the whole run (e.g. a Freesound sound turned out NC).
type skippableError struct{ reason string }

func (e skippableError) Error() string { return e.reason }

// isSkippable reports whether err is a skippableError and its reason.
func isSkippable(err error) (bool, string) {
	var se skippableError
	if ok := asSkippable(err, &se); ok {
		return true, se.reason
	}
	// Freesound non-redistributable license is a skip, not a hard failure.
	if strings.Contains(err.Error(), "non-redistributable license") {
		return true, err.Error()
	}
	return false, ""
}

// asSkippable unwraps err looking for a skippableError.
func asSkippable(err error, target *skippableError) bool {
	for err != nil {
		if se, ok := err.(skippableError); ok {
			*target = se
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
