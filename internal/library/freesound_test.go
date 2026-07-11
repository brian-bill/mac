package library

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeFreesoundLicense(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"http://creativecommons.org/publicdomain/zero/1.0/", "CC0-1.0"},
		{"https://creativecommons.org/licenses/by/4.0/", "CC-BY-4.0"},
		{"http://creativecommons.org/licenses/by-nc/3.0/", "CC-BY-NC"},
		{"http://creativecommons.org/licenses/by-sa/3.0/", "CC-BY-SA"},
		{"http://creativecommons.org/licenses/sampling+/1.0/", "Sampling+"},
	}
	for _, tc := range tests {
		if got := normalizeFreesoundLicense(tc.raw); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestIsRedistributable(t *testing.T) {
	yes := []string{"CC0-1.0", "CC-BY-4.0", "CC-BY-3.0"}
	no := []string{"CC-BY-NC", "CC-BY-SA", "Sampling+", "Proprietary", ""}
	for _, l := range yes {
		if !isRedistributable(l) {
			t.Errorf("%q should be redistributable", l)
		}
	}
	for _, l := range no {
		if isRedistributable(l) {
			t.Errorf("%q should NOT be redistributable", l)
		}
	}
}

func TestFreesoundResolveDownloadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/sounds/111/"):
			w.Write([]byte(`{"id":111,"name":"kick","username":"alice",
			  "license":"http://creativecommons.org/publicdomain/zero/1.0/",
			  "download":"` + "http://dl/111" + `"}`))
		case strings.Contains(r.URL.Path, "/sounds/222/"):
			w.Write([]byte(`{"id":222,"name":"loop","username":"bob",
			  "license":"http://creativecommons.org/licenses/by-nc/3.0/",
			  "download":"http://dl/222"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newFreesoundClient("tok", srv.Client())
	c.apiURL = srv.URL

	t.Run("cc0 resolves", func(t *testing.T) {
		url, lic, attr, err := c.resolveDownloadURL(111)
		if err != nil {
			t.Fatal(err)
		}
		if url != "http://dl/111" || lic != "CC0-1.0" {
			t.Fatalf("got url=%q lic=%q", url, lic)
		}
		if !strings.Contains(attr, "alice") || !strings.Contains(attr, "111") {
			t.Fatalf("attribution = %q", attr)
		}
	})

	t.Run("nc rejected", func(t *testing.T) {
		_, _, _, err := c.resolveDownloadURL(222)
		if err == nil || !strings.Contains(err.Error(), "non-redistributable") {
			t.Fatalf("got %v, want non-redistributable", err)
		}
	})

	t.Run("no token", func(t *testing.T) {
		nt := newFreesoundClient("", srv.Client())
		nt.apiURL = srv.URL
		_, _, _, err := nt.resolveDownloadURL(111)
		if err == nil || !strings.Contains(err.Error(), "FREESOUND_API_TOKEN") {
			t.Fatalf("got %v, want token error", err)
		}
	})
}
