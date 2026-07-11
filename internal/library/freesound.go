package library

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// freesoundAPIBase is the Freesound APIv2 root. Overridable in tests.
var freesoundAPIBase = "https://freesound.org/apiv2"

// freesoundSound is the subset of the Freesound APIv2 /sounds/{id}/ response we
// consume: the license URL/name and the preview download locations.
type freesoundSound struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	License  string `json:"license"`
	Username string `json:"username"`
	Previews struct {
		HighQualityWAV string `json:"preview-hq-ogg"` // ogg fallbacks exist; we prefer wav previews below
		LowQualityWAV  string `json:"preview-lq-ogg"`
	} `json:"previews"`
	Download string `json:"download"`
}

// freesoundClient talks to the Freesound APIv2 with a token.
type freesoundClient struct {
	token  string
	http   *http.Client
	apiURL string
}

// newFreesoundClient builds a client. An empty token is allowed at construction;
// requests will fail with a clear message if one is actually needed.
func newFreesoundClient(token string, hc *http.Client) *freesoundClient {
	return &freesoundClient{token: token, http: hc, apiURL: freesoundAPIBase}
}

// resolveDownloadURL fetches sound metadata for soundID, verifies the license is
// redistribution-safe, and returns a URL from which the sample bytes can be
// downloaded together with the resolved license and attribution string.
func (c *freesoundClient) resolveDownloadURL(soundID int) (url, license, attribution string, err error) {
	if c.token == "" {
		return "", "", "", fmt.Errorf("freesound sound %d requires FREESOUND_API_TOKEN to be set", soundID)
	}
	meta, err := c.sound(soundID)
	if err != nil {
		return "", "", "", err
	}
	lic := normalizeFreesoundLicense(meta.License)
	if !isRedistributable(lic) {
		return "", "", "", fmt.Errorf(
			"freesound sound %d has non-redistributable license %q (only CC0/CC-BY are accepted)",
			soundID, meta.License)
	}
	// Prefer a token-authorized original download; fall back to the HQ preview.
	dl := meta.Download
	if dl == "" {
		dl = meta.Previews.HighQualityWAV
	}
	if dl == "" {
		return "", "", "", fmt.Errorf("freesound sound %d exposes no download or preview URL", soundID)
	}
	attr := fmt.Sprintf("%s (freesound.org/s/%d)", meta.Username, soundID)
	return dl, lic, attr, nil
}

// sound performs GET /sounds/{id}/ and decodes the metadata.
func (c *freesoundClient) sound(soundID int) (*freesoundSound, error) {
	url := fmt.Sprintf("%s/sounds/%d/", c.apiURL, soundID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("freesound request for sound %d: %w", soundID, err)
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("freesound request for sound %d: %w", soundID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("freesound sound %d: HTTP %d", soundID, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("freesound sound %d: read body: %w", soundID, err)
	}
	var s freesoundSound
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("freesound sound %d: decode metadata: %w", soundID, err)
	}
	return &s, nil
}

// authorizedGet issues a token-authorized GET, needed for original downloads
// (previews are public). It returns the response for the caller to stream/close.
func (c *freesoundClient) authorizedGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Token "+c.token)
	}
	return c.http.Do(req)
}

// normalizeFreesoundLicense maps a Freesound license URL/name to a short SPDX-ish
// token used by isRedistributable and the catalog.
func normalizeFreesoundLicense(raw string) string {
	l := strings.ToLower(raw)
	switch {
	case strings.Contains(l, "publicdomain") || strings.Contains(l, "zero") || strings.Contains(l, "cc0"):
		return "CC0-1.0"
	case strings.Contains(l, "by-nc"):
		return "CC-BY-NC"
	case strings.Contains(l, "by-sa"):
		return "CC-BY-SA"
	case strings.Contains(l, "/by/") || strings.HasSuffix(l, "by") || strings.Contains(l, "attribution"):
		return "CC-BY-4.0"
	case strings.Contains(l, "sampling"):
		return "Sampling+"
	default:
		return raw
	}
}

// isRedistributable reports whether a normalized license may be committed to the
// default (redistributed) tier: only CC0 and plain CC-BY qualify.
func isRedistributable(license string) bool {
	switch license {
	case "CC0-1.0", "CC-BY-4.0", "CC-BY-3.0":
		return true
	default:
		return false
	}
}
