// Package cmd wires up the "mac" command-line interface.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// dbPath is the path to the SQLite instrument registry, shared by every
// subcommand via the persistent --db flag.
var dbPath string

// instrumentsPath is the directory scanned for instrument manifests, shared by
// every subcommand via the persistent --instruments flag.
var instrumentsPath string

// rootCmd is the base command for the mac CLI.
var rootCmd = &cobra.Command{
	Use:   "mac",
	Short: "mac — a Music-as-Code composition compiler",
	Long: `mac compiles text-based .bt compositions into audio.

Musical tracks are written as human-readable .bt files organized in a beats
directory. mac parses them and renders deterministic audio output.`,
}

// Execute runs the root command, exiting with a non-zero status on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "./mac.db",
		"path to the SQLite instrument registry")
	rootCmd.PersistentFlags().StringVar(&instrumentsPath, "instruments", "./instruments",
		"directory scanned for instrument manifests")

	// Cobra prints command usage on RunE errors by default, which is noisy for
	// runtime failures (bad directory, DB errors). Silence it globally; the
	// error message itself is still printed.
	rootCmd.SilenceUsage = true
}
