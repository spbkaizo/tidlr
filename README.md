# tidlr

A fast, native-Go pipeline for building a lossless music library from Tidal.

`tidlr` downloads albums, playlists and individual tracks from Tidal as
**lossless FLAC**, converts
them to **ALAC** (`.m4a`) with embedded cover art and tags, and files them under
a tidy `<Artist>/<Album>/` tree. It can also keep up with new releases
automatically by scraping [AnyDecentMusic](http://www.anydecentmusic.com)'s
"Recent Releases" chart and downloading everything new.

Everything Tidal — authentication, search, edition matching, and downloading —
is implemented natively in Go. There is **no Python and no external downloader**;
the only runtime dependency is `ffmpeg`.

---

## Features

- **Lossless first.** Downloads FLAC and transcodes to ALAC (a lossless Apple
  format). Prefers a lossless **stereo** master, transparently avoiding lossy
  Dolby Atmos editions even when Tidal's API defaults to them.
- **Smart edition matching.** When resolving an album by name, `tidlr` searches
  Tidal and then scans the artist's catalogue to pick the best edition —
  lossless over Atmos, explicit (original) over clean, newest over oldest —
  finding lossless masters that Tidal's own search hides.
- **Direct downloads.** Grab any album or playlist by URL/ID.
- **Release tracking.** Scrape AnyDecentMusic for new albums; a persistent
  library records what you already have so nothing is downloaded twice.
- **Resilient.** Concurrent downloads with per-track retries; a single bad track
  or album never sinks a batch. Interrupt any time and re-run to resume.
- **Cover art + tags.** Every track gets embedded artwork and
  title/artist/album/track metadata.

---

## Requirements

- **Go 1.26+** (to build/install)
- **ffmpeg** (for FLAC→ALAC conversion) — `brew install ffmpeg`
- **A paid Tidal subscription** (HiFi/HiFi Plus for lossless)

---

## Install

```sh
git clone <this-repo> ~/src/tidlr   # or however you obtained it
cd ~/src/tidlr
go install ./cmd/tidlr
```

This builds `tidlr` into `$(go env GOPATH)/bin`. Make sure that directory is on
your `PATH` (it usually is). Verify:

```sh
tidlr            # prints usage
```

---

## Quick start

```sh
# 1. Authenticate once (opens a Tidal device-authorization link)
tidlr login

# 2. Download a single album
tidlr --album https://tidal.com/album/540168117

# 3. Download a playlist
tidlr --playlist https://tidal.com/playlist/f98d7491-56e3-4b96-b536-60c1d2e5759e

# 4. Download individual tracks
tidlr --track https://tidal.com/track/113302335

# 5. Or catch up on new releases from AnyDecentMusic
tidlr sync
```

Files land in `~/Music/tidlr/` by default. See the
**[User Guide](docs/USER_GUIDE.md)** for everything else.

---

## Commands at a glance

| Command / flag                | What it does                                                        |
| ----------------------------- | ------------------------------------------------------------------- |
| `tidlr login`                 | Authenticate with Tidal (device-code flow). Needed once.            |
| `tidlr --album <url\|id ...>` | Download one or more albums into `<out>/<Artist>/<Album>/`.          |
| `tidlr --playlist <url\|uuid>`| Download a playlist into `<out>/playlist/<name>/`.                   |
| `tidlr --track <url\|id ...>` | Download one or more individual tracks into `<out>/tracks/`.        |
| `tidlr sync`                  | Scrape new AnyDecentMusic releases, then download them.             |
| `tidlr scrape`                | Just enqueue new releases (with `--since` to reach further back).   |
| `tidlr run`                   | Download everything currently queued.                               |
| `tidlr retry`                 | Requeue failed items and run again.                                 |
| `tidlr status`                | Show queue counts (pending / downloading / done / failed).          |

Global flags: `--config <path>`, `--since <DDMMYY>`, `--force`.

---

## How it works

```
                    ┌─────────────── tidlr sync / scrape ───────────────┐
                    │                                                    │
AnyDecentMusic ─► scrape ─► queue (SQLite) ─┐                            │
 (new releases)             + dedupe        │                            │
                                            ▼                            │
                             ┌── matcher ──► pick best lossless edition   │
tidlr --album/--playlist ───►│              (search + artist catalogue)   │
 (direct)                    ▼                                            │
                       Tidal download ─► FLAC ─► ffmpeg ─► ALAC + art ─► ~/Music/tidlr
                       (native Go)                                        │
                                                                         │
                    └────────────────────────────────────────────────────┘
```

- **`internal/tidal`** — native Tidal client: OAuth device-flow auth + refresh,
  REST API (search, album, artist-albums, playlist, track streams), stream
  manifest parsing, and the concurrent downloader.
- **`internal/adm`** — scrapes AnyDecentMusic's chart (paginated, dated).
- **`internal/queue`** — SQLite work-queue plus a permanent "downloads" library
  for dedupe.
- **`internal/convert`** — ffmpeg FLAC→ALAC with cover embedding.
- **`internal/downloader`** — glues matching + downloading behind one interface.
- **`internal/pipeline`** — bounded worker pools tying the stages together.
- **`cmd/tidlr`** — the CLI.

---

## Configuration

`tidlr` runs with sensible defaults and needs no config file. To customise, copy
`config.example.toml` to `config.toml` (or pass `--config <path>`):

```toml
output_dir = "~/Music/tidlr"   # where ALAC files land
quality    = "max"             # low | normal | high | max  (max = HiRes lossless)
keep_flac  = false             # true also puts the source FLACs in the library
```

See `config.example.toml` for all options and the
**[User Guide](docs/USER_GUIDE.md)** for details.

---

## Notes & limitations

- **AnyDecentMusic reach:** the `--since` scrape can only go back as far as ADM's
  chart lists (~6 weeks); older albums aren't exposed on the site.
- **Atmos-only albums:** if an album genuinely has no stereo master (only Dolby
  Atmos), `tidlr` delivers the lossy `.mp4` (eac3) as a fallback rather than
  skipping it.
- **Personal use:** intended for building a personal library from content you're
  entitled to via your own subscription.

---

## Changelog

v1.0.0 is a complete, ground-up rewrite of the original 0.9.x utility. See
[CHANGELOG.md](CHANGELOG.md) for details; the pre-1.0 code lives on the
[`legacy-0.9`](https://github.com/spbkaizo/tidlr/tree/legacy-0.9) branch.
