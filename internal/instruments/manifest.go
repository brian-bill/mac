// Package instruments discovers instrument definitions on disk and registers
// them into the SQLite instrument library.
//
// An instrument is any directory containing a manifest.json that declares its
// ID, display name, and a .wav sample. Dropping such a folder into the scan
// root and re-running a scan makes the instrument available to .bt tracks,
// satisfying the dynamic-extensibility goal.
package instruments

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// manifestFileName is the marker file that turns a directory into an
// instrument folder.
const manifestFileName = "manifest.json"

// idPattern constrains instrument IDs so that every registered instrument is
// referenceable as a .bt track header ([Kick]) without escaping: a leading
// letter followed by letters, digits, or underscores.
var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Manifest is the on-disk instrument declaration parsed from manifest.json.
type Manifest struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Sample string          `json:"sample"`
	Config json.RawMessage `json:"config,omitempty"`
}

// ParseManifest reads and decodes the manifest.json at path. It does not
// validate the semantic rules (see validate) — only that the file is readable
// and syntactically valid JSON.
func ParseManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return m, nil
}

// validate enforces the semantic rules for a manifest located in manifestDir
// and returns the absolute resolved sample path and the compact config JSON to
// store. All errors name the manifest's directory so diagnostics are
// actionable.
func (m Manifest) validate(manifestDir string) (samplePath, configJSON string, err error) {
	manifestPath := filepath.Join(manifestDir, manifestFileName)

	if !idPattern.MatchString(m.ID) {
		return "", "", fmt.Errorf(
			"invalid id %q in %s: must match %s (a letter followed by letters, digits, or underscores)",
			m.ID, manifestPath, idPattern.String(),
		)
	}

	if strings.TrimSpace(m.Name) == "" {
		return "", "", fmt.Errorf("missing name in %s", manifestPath)
	}

	if strings.TrimSpace(m.Sample) == "" {
		return "", "", fmt.Errorf("missing sample in %s", manifestPath)
	}

	samplePath, err = resolveSample(manifestDir, m.Sample)
	if err != nil {
		return "", "", fmt.Errorf("%w (declared in %s)", err, manifestPath)
	}

	configJSON, err = normalizeConfig(m.Config)
	if err != nil {
		return "", "", fmt.Errorf("invalid config JSON in %s: %w", manifestPath, err)
	}

	return samplePath, configJSON, nil
}

// resolveSample resolves rel against manifestDir, guarding against path
// traversal outside the instrument folder and confirming the target is an
// existing .wav file.
func resolveSample(manifestDir, rel string) (string, error) {
	absDir, err := filepath.Abs(manifestDir)
	if err != nil {
		return "", fmt.Errorf("resolve instrument directory %q: %w", manifestDir, err)
	}

	resolved := filepath.Join(absDir, rel)

	// Path-traversal guard: the resolved sample must live inside the
	// instrument folder. filepath.Join has already cleaned any "..".
	if resolved != absDir && !strings.HasPrefix(resolved, absDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("sample %q escapes its instrument folder", rel)
	}

	if !strings.EqualFold(filepath.Ext(resolved), ".wav") {
		return "", fmt.Errorf("unsupported sample extension for %q: only .wav is allowed", rel)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("sample file does not exist: %s", resolved)
		}
		return "", fmt.Errorf("stat sample %s: %w", resolved, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("sample path is a directory: %s", resolved)
	}

	return resolved, nil
}

// normalizeConfig returns the compact JSON form of raw, defaulting to "{}" when
// the config is absent. Syntactically invalid JSON is rejected.
func normalizeConfig(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("config is not valid JSON")
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", fmt.Errorf("compact config JSON: %w", err)
	}
	return compact.String(), nil
}
