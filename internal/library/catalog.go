// Package library builds mac's default instrument library. It is a build-time
// tool, separate from the audio compiler: it fetches sound samples from curated
// remote sources, normalizes them to the engine's canonical WAV format, and
// writes them into the instruments/ tree as ordinary instrument folders
// (manifest.json + .wav) that Spec 002's scanner registers.
//
// The compiler itself never imports this package and never touches the network;
// determinism (GOAL.md §4) is preserved because the committed WAVs — pinned by
// SHA-256 in the catalog — are the sole input to compilation. See
// specs/006-default-sound-library.md.
package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// idPattern mirrors internal/instruments.idPattern so a catalog entry's ID is
// guaranteed usable as a .bt track header ([Kick]) once vendored.
var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Tier partitions sources by redistribution safety. Default-tier instruments are
// committed to the repo; restricted-tier instruments are fetched only to a
// git-ignored directory and never committed (see the licensing section of the
// spec).
const (
	TierDefault    = "default"
	TierRestricted = "restricted"
)

// Provider selects the download strategy for a source.
const (
	ProviderGitHub    = "github"
	ProviderFreesound = "freesound"
	ProviderHTTP      = "http"
)

// Catalog is the parsed sources/catalog.json: the reproducible recipe the fetch
// tool consumes.
type Catalog struct {
	CanonicalFormat CanonicalFormat `json:"canonical_format"`
	Instruments     []Entry         `json:"instruments"`
}

// CanonicalFormat records the target format in the catalog for documentation and
// a sanity check against the compiled-in constants.
type CanonicalFormat struct {
	SampleRate int `json:"sample_rate"`
	Channels   int `json:"channels"`
	BitDepth   int `json:"bit_depth"`
}

// Entry is a single instrument recipe.
type Entry struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Category  string          `json:"category"`
	Tier      string          `json:"tier"`
	Source    Source          `json:"source"`
	Transform Transform       `json:"transform"`
	SHA256    string          `json:"sha256"`
	Config    json.RawMessage `json:"config,omitempty"`
}

// Source declares where a sample comes from and under what license.
type Source struct {
	Provider    string `json:"provider"`
	URL         string `json:"url,omitempty"`
	SoundID     int    `json:"sound_id,omitempty"`
	License     string `json:"license"`
	Attribution string `json:"attribution,omitempty"`
}

// Transform is the deterministic post-download processing for an entry.
type Transform struct {
	NormalizePeakDBFS *float64 `json:"normalize_peak_dbfs,omitempty"`
	TrimSilence       bool     `json:"trim_silence,omitempty"`
}

// options converts an entry's Transform into TranscodeOptions.
func (t Transform) options() TranscodeOptions {
	return TranscodeOptions{
		NormalizePeakDBFS: t.NormalizePeakDBFS,
		TrimSilence:       t.TrimSilence,
	}
}

// LoadCatalog reads, parses, and validates the catalog at path.
func LoadCatalog(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}
	var cat Catalog
	// DisallowUnknownFields would make the catalog brittle to additive changes;
	// we accept unknown fields and validate the ones we require.
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	return &cat, nil
}

// Validate enforces the catalog's semantic rules: a sane canonical format, and
// per-entry ID grammar, license presence, known tier/provider, safe category
// path, and globally unique IDs. The first violation aborts with an error that
// names the offending entry, mirroring the fail-fast scan in Spec 002.
func (c *Catalog) Validate() error {
	if c.CanonicalFormat.SampleRate != canonicalSampleRate ||
		c.CanonicalFormat.Channels != canonicalChannels ||
		c.CanonicalFormat.BitDepth != canonicalBitDepth {
		return fmt.Errorf(
			"catalog canonical_format is %dHz/%dch/%dbit but the engine requires %dHz/%dch/%dbit",
			c.CanonicalFormat.SampleRate, c.CanonicalFormat.Channels, c.CanonicalFormat.BitDepth,
			canonicalSampleRate, canonicalChannels, canonicalBitDepth)
	}
	if len(c.Instruments) == 0 {
		return fmt.Errorf("catalog contains no instruments")
	}

	seen := make(map[string]struct{}, len(c.Instruments))
	for i := range c.Instruments {
		e := &c.Instruments[i]
		if err := e.validate(); err != nil {
			return err
		}
		if _, dup := seen[e.ID]; dup {
			return fmt.Errorf("duplicate instrument id %q in catalog", e.ID)
		}
		seen[e.ID] = struct{}{}
	}
	return nil
}

// validate checks a single entry.
func (e *Entry) validate() error {
	if !idPattern.MatchString(e.ID) {
		return fmt.Errorf("invalid id %q: must match %s", e.ID, idPattern.String())
	}
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("entry %q: missing name", e.ID)
	}
	switch e.Tier {
	case TierDefault, TierRestricted:
	case "":
		return fmt.Errorf("entry %q: missing tier (want %q or %q)", e.ID, TierDefault, TierRestricted)
	default:
		return fmt.Errorf("entry %q: unknown tier %q", e.ID, e.Tier)
	}
	switch e.Source.Provider {
	case ProviderGitHub, ProviderFreesound, ProviderHTTP:
	case "":
		return fmt.Errorf("entry %q: missing source.provider", e.ID)
	default:
		return fmt.Errorf("entry %q: unknown source.provider %q", e.ID, e.Source.Provider)
	}
	if strings.TrimSpace(e.Source.License) == "" {
		return fmt.Errorf("entry %q: empty source.license (unknown-license samples are rejected)", e.ID)
	}
	// Provider-specific location requirement.
	switch e.Source.Provider {
	case ProviderFreesound:
		if e.Source.SoundID <= 0 {
			return fmt.Errorf("entry %q: freesound source needs a positive sound_id", e.ID)
		}
	default: // github, http
		if strings.TrimSpace(e.Source.URL) == "" {
			return fmt.Errorf("entry %q: %s source needs a url", e.ID, e.Source.Provider)
		}
	}
	// http provider is restricted-tier only (cymatics/99sounds/goldbay EULAs).
	if e.Source.Provider == ProviderHTTP && e.Tier != TierRestricted {
		return fmt.Errorf("entry %q: http-provider sources must be tier %q, not %q", e.ID, TierRestricted, e.Tier)
	}
	if err := validateCategory(e.Category); err != nil {
		return fmt.Errorf("entry %q: %w", e.ID, err)
	}
	if len(e.Config) > 0 && !json.Valid(e.Config) {
		return fmt.Errorf("entry %q: config is not valid JSON", e.ID)
	}
	return nil
}

// validateCategory guards the category subpath against traversal and absolute
// paths so a catalog entry can never write outside the output root.
func validateCategory(category string) error {
	if strings.TrimSpace(category) == "" {
		return fmt.Errorf("missing category")
	}
	if filepath.IsAbs(category) {
		return fmt.Errorf("category %q must be relative", category)
	}
	clean := filepath.Clean(category)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("category %q escapes the output root", category)
	}
	return nil
}
