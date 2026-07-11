package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/bryanbill/mac/internal/db"
	"github.com/bryanbill/mac/internal/instruments"
	"github.com/bryanbill/mac/internal/library"
	"github.com/spf13/cobra"
)

// fetch command flags.
var (
	catalogPath     string
	allowRestricted bool
	updateHashes    bool
	offlineVerify   bool
	onlyIDs         string
)

var fetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Download and vendor the default instrument library from the source catalog",
	Long: `fetch reads sources/catalog.json, downloads each declared sample, normalizes
it to the engine's canonical WAV format (44100 Hz / mono / 16-bit PCM), and writes
it into the instruments directory as a ready-to-use instrument folder
(manifest.json + .wav).

The compiler never touches the network: fetch runs once at setup time and the
resulting WAVs — pinned by SHA-256 in the catalog — are what compilation reads.
Re-running fetch is idempotent; --offline-verify re-hashes the vendored library
without any network access (the determinism gate).`,
	Args: cobra.NoArgs,
	RunE: runFetch,
}

func init() {
	fetchCmd.Flags().StringVar(&catalogPath, "catalog", "sources/catalog.json",
		"path to the source catalog")
	fetchCmd.Flags().BoolVar(&allowRestricted, "allow-restricted", false,
		"also fetch restricted-tier sources into instruments/_restricted (never committed)")
	fetchCmd.Flags().BoolVar(&updateHashes, "update-hashes", false,
		"fill in absent sha256 fields for new catalog entries")
	fetchCmd.Flags().BoolVar(&offlineVerify, "offline-verify", false,
		"do not download; only verify vendored WAVs against the catalog hashes")
	fetchCmd.Flags().StringVar(&onlyIDs, "only", "",
		"comma-separated instrument IDs to limit the run to")
	rootCmd.AddCommand(fetchCmd)
}

func runFetch(cmd *cobra.Command, args []string) error {
	cat, err := library.LoadCatalog(catalogPath)
	if err != nil {
		return err
	}

	only := map[string]struct{}{}
	for _, id := range strings.Split(onlyIDs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = struct{}{}
		}
	}

	out := cmd.OutOrStdout()
	opts := library.FetchOptions{
		OutRoot:         instrumentsPath,
		AllowRestricted: allowRestricted,
		UpdateHashes:    updateHashes,
		OfflineVerify:   offlineVerify,
		Only:            only,
		CatalogPath:     catalogPath,
		FreesoundToken:  os.Getenv("FREESOUND_API_TOKEN"),
		Out:             out,
	}

	report, err := cat.Fetch(opts)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\nfetched:  %d\nverified: %d\nskipped:  %d\n",
		len(report.Fetched), len(report.Verified), len(report.Skipped))
	if len(report.Updated) > 0 {
		fmt.Fprintf(out, "hashes filled: %d\n", len(report.Updated))
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}

	// Register the freshly vendored library so the DB reflects it, unless we were
	// only verifying (no library mutation) — but registering is harmless and
	// confirms the whole set scans cleanly, so do it except in offline-verify.
	if !offlineVerify {
		database, dberr := db.Open(dbPath)
		if dberr != nil {
			return dberr
		}
		defer database.Close()
		n, rerr := instruments.Register(database, instrumentsPath)
		if rerr != nil {
			return fmt.Errorf("register vendored library: %w", rerr)
		}
		fmt.Fprintf(out, "registered instruments: %d\n", n)
	}
	return nil
}
