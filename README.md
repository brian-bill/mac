<div align="center">

# Music as Code

**Compose full, multi-instrument tracks.**

![Language](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Coverage](https://img.shields.io/badge/coverage-85%25%2B-brightgreen)
![License](https://img.shields.io/badge/license-MIT-blue)

</div>

## Overview

**Mac** (Music as Code) is a composition tool that lets you write music the way you
write software. A track is just a human-readable, version-controllable text file
in a simple domain-specific language (`.bt`). Mac parses those files and renders
them into deterministic audio, the same input always produces the same
output.

## Setup

**Prerequisites:** Go 1.26 or newer. The SQLite driver is
[`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) (pure Go), so **no
C toolchain or CGO is required**.

```sh
git clone <repo-url> mac
cd mac
go build -o mac .       
```

Smoke-test it against the committed demo beat and the bundled instrument library:

```sh
./mac compile ./beats -o demo.mp3
```

Two persistent flags are available on every subcommand:

| Flag | Default | Purpose |
|------|---------|---------|
| `--db` | `./mac.db` | Path to the SQLite instrument registry |
| `--instruments` | `./instruments` | Directory scanned for instrument manifests |

## Writing beats

A `.bt` file has two kinds of blocks: a **metadata** block at the top, then one
or more **track** blocks.

### Metadata block

Must come first, one `key: value` per line. Required keys are `bpm` and
`signature`:

```
bpm: 120
signature: 4/4
```

### Track blocks

Each track is an instrument header in square brackets followed by a
space-separated grid of subdivisions:

- **Header** — `[InstrumentID]` must match a registered instrument's `id` exactly
  (e.g. `[Kick]`, `[HatClosed]`).
- **Numbers** (`1`, `2`, `3`, …) — a hit on that beat.
- **Dots** (`.`) — a rest (silence) on that subdivision.
- **Letters** (optional) — velocity mapping, e.g. `A` for a loud hit and `a` for
  a soft one (numeric hits play at full velocity).

Formatting rules: put a blank line between tracks, trailing spaces are ignored,
and comments are not yet supported (kept minimal for the MVP lexer).

### Worked example ([`beats/demo.bt`](beats/demo.bt))

```
bpm: 75
signature: 4/4

[Kick]
. . . . . . . . 3 . . . . . . .

[HatClosed]
1 . 2 . 3 . 4 . 1 . 2 . 3 . 4 .

[SnareRim]
. . . . . . . . 3 . . . . . . .

[Rim808]
. . . . . . . . . . . . 4 . . .

[HatOpen]
. . . . . . . . . . . . . . 4 .

[BassSub]
1 . . . . . . . 3 . . . . . . .
```

Compile a directory of `.bt` files into a playable audio file:

```sh
./mac compile ./beats -o output.mp3
```

Each `.bt` file is one section; sections concatenate in sorted filename order
to form a continuous piece, and the result is a real, playable `.mp3`.

## Sound library

`mac` ships with a comprehensive, batteries-included instrument library so you
can write and compile beats immediately. The samples are **vendored** into
`instruments/` as ordinary instrument folders (`manifest.json` + `.wav`) — the
exact layout the compiler already scans.

### Canonical audio format

Every vendored sample is normalized to **44100 Hz / mono / 16-bit PCM WAV**. This
is mandatory: the mixer rejects any render whose
samples disagree on sample rate or channel count, so the whole library must share
one format. The fetch tool downmixes stereo to mono, resamples to 44100, and
requantizes to 16-bit

### The source catalog

[`sources/catalog.json`](sources/catalog.json) is the reproducible recipe. Each
entry declares an instrument's ID, category, source URL, license, and the SHA-256
of the final normalized WAV. On fetch, the hash is **verified** (drift fails
loudly) or **filled in** (`--update-hashes` for a newly added entry).

```jsonc
{
  "canonical_format": { "sample_rate": 44100, "channels": 1, "bit_depth": 16 },
  "instruments": [
    {
      "id": "Kick808",
      "name": "TR-808 Bass Drum",
      "category": "drums/808",
      "tier": "default",
      "source": {
        "provider": "github",
        "url": "https://raw.githubusercontent.com/<owner>/<repo>/<sha>/BD/BD0000.WAV",
        "license": "Public-Domain",
        "attribution": "..."
      },
      "transform": { "normalize_peak_dbfs": -1.0, "trim_silence": true },
      "sha256": "<hash of the final normalized wav>",
      "config": { "gain": 1.0 }
    }
  ]
}
```

Providers:

- **`github`** — raw file pinned to an immutable commit SHA. The backbone of the
  default tier.
- **`freesound`** — Freesound APIv2 (`sound_id`). Only CC0/CC-BY licenses are accepted; NC/unknown are
  skipped with a warning.
- **`http`** — a generic direct URL. **Restricted tier only** (see Licensing).

### Licensing tiers

| Tier | Committed to repo? | Gate |
|------|--------------------|------|
| `default` | ✅ Yes | Always fetched |
| `restricted` | ❌ No — written to git-ignored `instruments/_restricted/` | `--allow-restricted` |

Restricted-tier sources (e.g. proprietary-EULA sample packs) are **never
committed**. They can be fetched for your own private, non-redistributed use with
`mac fetch --allow-restricted`, landing in `instruments/_restricted/` which is
git-ignored. The fetch tool refuses any entry with an empty/unknown license.

CC-BY samples are credited in the generated
[`instruments/ATTRIBUTION.md`](instruments/ATTRIBUTION.md).

## Instruments

### Consuming an instrument

An instrument is simply a folder containing a `manifest.json` and a `.wav`
sample. The `id` in the manifest is what you reference as a track header in a
`.bt` file.

```json
{
  "id": "Clap",
  "name": "Hand Clap",
  "sample": "Clap.wav",
  "config": {
    "gain": 1.0
  }
}
```

| Field | Required | Rules |
|-------|----------|-------|
| `id` | ✅ | A letter followed by letters, digits, or underscores (e.g. `Kick808`). Referenced as `[id]` in tracks. |
| `name` | ✅ | Human-readable display name. |
| `sample` | ✅ | Relative path to a `.wav` file inside the folder (cannot escape the folder). |
| `config` | — | Optional JSON, e.g. `{ "gain": 1.0 }`. |

Instruments are discovered and registered into the SQLite registry automatically
whenever `compile` or `fetch` scans the `--instruments` directory.

### The bundled default library

Mac ships **batteries-included**: 110 instruments are vendored under
`instruments/` (kicks/snares/hats, TR-808 / TR-909 / LinnDrum classics, world
percussion, melodic basses, and FX). They are committed to the repo, so a fresh
clone can compose immediately — no download step needed.

Every sample is normalized to the engine's canonical format: **44100 Hz / mono /
16-bit PCM WAV**.

To (re)build the library from its reproducible source catalog
([`sources/catalog.json`](sources/catalog.json)):

```sh
./mac fetch                    # download + normalize + vendor default-tier instruments
./mac fetch --offline-verify   # re-hash vendored WAVs against the catalog (no network)
```

`mac fetch` flags:

| Flag | Purpose |
|------|---------|
| `--catalog <path>` | Source catalog (default `sources/catalog.json`) |
| `--allow-restricted` | Also fetch restricted-tier sources into git-ignored `instruments/_restricted/` |
| `--update-hashes` | Fill in absent `sha256` fields for new catalog entries |
| `--offline-verify` | No network; verify vendored WAVs against catalog hashes |
| `--only <id,id,…>` | Limit the run to specific instrument IDs |

See [`docs/sound-library.md`](docs/sound-library.md) for the full design.

## Adding new content

**Add a new beat** — drop a `.bt` file into `beats/` following the grammar above,
then recompile:

```sh
./mac compile ./beats -o output.mp3
```

**Add a local instrument** — create a folder with a manifest and a `.wav`; it
auto-registers on the next scan:

```
instruments/percussion/mydrum/
├── manifest.json      # { "id": "MyDrum", "name": "My Drum", "sample": "MyDrum.wav" }
└── MyDrum.wav
```

**Add a reproducible/shared instrument** via the source catalog:

1. Add an entry to `sources/catalog.json` with an empty `"sha256": ""`.
2. Run `./mac fetch --update-hashes --only MyNewID` to download, normalize, and
   seed the hash.
3. Commit the new `instruments/…` folder and the updated `catalog.json`.

## Project layout

```
cmd/                CLI commands (cobra): root, compile, fetch
internal/
  bt/               .bt lexer, parser, and AST
  schedule/         flattens a Composition into timed AudioEvents
  audio/            sample loading, mixing, and MP3 encoding
  instruments/      manifest parsing + registry scanning
  library/          source catalog fetch/transcode/verify
  db/               SQLite instrument registry
beats/              .bt compositions (demo.bt)
instruments/        vendored default instrument library (manifest.json + .wav)
sources/            catalog.json — the reproducible library recipe
```

## Testing

```sh
go test ./...            # run the suite
go test ./... -cover     # with per-package coverage
```

## Licensing

**Bundled samples.** Every default-tier instrument in this repo is sourced from
Public-Domain / CC0 collections — the
[TidalCycles Dirt-Samples](https://github.com/tidalcycles/Dirt-Samples) library
and a [TR-808/909 & LinnDrum classic archive](https://github.com/CarlosIrineuCosta/tr808-drum).
Samples requiring credit are listed in
[`instruments/ATTRIBUTION.md`](instruments/ATTRIBUTION.md). Restricted-tier
sources are never committed to the repository.

**Project code.** This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
