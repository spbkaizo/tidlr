# tidlr — User Guide

This guide covers everything you can do with `tidlr`, from first login to keeping
a music library up to date. For a high-level overview, see the
[README](../README.md).

## Contents

1. [Installation](#1-installation)
2. [First-time setup: logging in](#2-first-time-setup-logging-in)
3. [Downloading a single album](#3-downloading-a-single-album)
4. [Downloading multiple albums at once](#4-downloading-multiple-albums-at-once)
5. [Downloading a playlist](#5-downloading-a-playlist)
6. [Keeping up with new releases (AnyDecentMusic)](#6-keeping-up-with-new-releases-anydecentmusic)
7. [The queue and the library](#7-the-queue-and-the-library)
8. [Configuration reference](#8-configuration-reference)
9. [Audio quality & formats](#9-audio-quality--formats)
10. [Troubleshooting](#10-troubleshooting)

---

## 1. Installation

Prerequisites: **Go 1.26+**, **ffmpeg**, and a **paid Tidal subscription**.

```sh
brew install ffmpeg          # if you don't have it
cd ~/src/tidlr
go install ./cmd/tidlr
```

`go install` places the `tidlr` binary in `$(go env GOPATH)/bin`. Confirm it's
runnable:

```sh
tidlr        # should print the usage screen
```

If `tidlr: command not found`, add Go's bin directory to your `PATH`:

```sh
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

---

## 2. First-time setup: logging in

Authenticate once with Tidal's device-authorization flow:

```sh
tidlr login
```

`tidlr` prints a URL like `https://link.tidal.com/ABCDE`. Open it in a browser,
approve access with your Tidal account, and return to the terminal — `tidlr`
polls until it's approved and saves the token.

The token is stored at `~/.tiddl/auth.json` and refreshes itself automatically,
so you should rarely need to log in again.

> **Already used another Tidal downloader?** If you have an existing
> `~/.tiddl/auth.json`, `tidlr` reuses it and you can skip `tidlr login`.

---

## 3. Downloading a single album

Pass a Tidal album URL (any form) or a bare album ID:

```sh
tidlr --album https://tidal.com/album/540168117
tidlr --album https://tidal.com/album/540168117/u   # trailing /u is fine
tidlr --album 540168117                              # bare ID works too
```

The album downloads to:

```
~/Music/tidlr/<Artist>/<Album>/NN - <Track>.m4a
```

Tracks are numbered (`01 - …`), tagged (title/artist/album/track), and carry
embedded cover art. Multi-disc albums prefix the disc number (`2-01 - …`).

---

## 4. Downloading multiple albums at once

`--album` accepts several URLs or IDs, separated by spaces or commas:

```sh
tidlr --album "540168117 498575262 491226198"
```

They download **sequentially**, each into its own `<Artist>/<Album>/` folder. The
run is resilient: if one album fails, `tidlr` logs it and continues with the
rest, finishing with a summary like:

```
albums: 17 done, 1 failed of 18
```

Re-run the failed one(s) individually if needed.

---

## 5. Downloading a playlist

Pass a playlist URL or UUID:

```sh
tidlr --playlist https://tidal.com/playlist/f98d7491-56e3-4b96-b536-60c1d2e5759e
```

The playlist downloads to:

```
~/Music/tidlr/playlist/<Playlist Name>/NN - <Artist> - <Track>.m4a
```

Because a playlist mixes tracks from many albums, filenames include the artist,
are numbered in playlist order, and each track keeps **its own** album's cover
art and album tag.

Playlist downloads are tolerant of individual failures — one unavailable track is
skipped and logged, and the rest are delivered.

---

## 6. Keeping up with new releases (AnyDecentMusic)

`tidlr` can build and maintain a library from
[AnyDecentMusic](http://www.anydecentmusic.com)'s "Recent Releases" chart.

### The everyday command

```sh
tidlr sync
```

This does two things: **scrape** (find new albums on ADM and enqueue the ones you
don't already have) and **run** (download + convert everything queued).

### Reaching further back

By default `sync`/`scrape` only look at ADM's "Just in" page. To pull everything
added since a date, use `--since` (format `DDMMYY` or `DD/MM/YYYY`):

```sh
tidlr --since 010226 sync     # everything added since 1 Feb 2026
```

> ADM's chart only spans roughly the last six weeks — `--since` can't reach
> older than whatever the site still lists.

### Running the stages separately

```sh
tidlr scrape                  # enqueue new releases, don't download yet
tidlr scrape --since 010226   # …reaching back to a date
tidlr status                  # see what's queued
tidlr run                     # download + convert the queue
tidlr retry                   # requeue anything that failed, then run
```

### Re-downloading

Albums already in your library are skipped on future scrapes. To force a
re-download (overwriting existing files):

```sh
tidlr --force --since 010226 sync
```

---

## 7. The queue and the library

`tidlr` keeps two things in `~/.local/share/tidlr/`:

- **A work queue** (`queue.db`) — the current batch of albums moving through the
  pipeline: `pending → downloading → converting → done` (or `failed`).
- **A permanent library record** — every album successfully downloaded. This is
  what lets a scrape skip things you already have, independently of the queue.

`tidlr status` shows the queue state:

```
queue:
  pending      3
  downloading  0
  converting   1
  done         36
  failed       1
  total        41
```

You can **interrupt any run** (Ctrl-C) and simply run again — items left
mid-flight are recovered automatically, and `tidlr retry` picks up failures.

The scratch download area (partial FLACs before conversion) also lives under
`~/.local/share/tidlr/` and is cleaned up automatically.

---

## 8. Configuration reference

`tidlr` works with no config file. To customise, copy the example and edit it:

```sh
cp config.example.toml config.toml
```

`tidlr` looks for `config.toml` in the current directory, or wherever you point
it with `--config <path>`.

| Key                | Default              | Meaning                                                         |
| ------------------ | -------------------- | --------------------------------------------------------------- |
| `output_dir`       | `~/Music/tidlr`      | Where final ALAC files land (`<Artist>/<Album>/…`).             |
| `work_dir`         | `~/.local/share/tidlr` | Holds the queue DB and the FLAC download scratch area.        |
| `ffmpeg_bin`       | `ffmpeg`             | Path to ffmpeg (change if it's not on your `PATH`).             |
| `auth_file`        | `~/.tiddl/auth.json` | Tidal token store. Blank reuses an existing login.             |
| `quality`          | `max`               | Track quality: `low` \| `normal` \| `high` \| `max`.            |
| `download_threads` | `8`                 | Concurrent track downloads within one album/playlist.          |
| `download_workers` | `3`                 | How many albums download at once (pipeline mode).              |
| `convert_workers`  | `4`                 | How many albums convert at once (pipeline mode).               |
| `keep_flac`        | `false`             | Keep the source FLACs after converting to ALAC.                |

`~` at the start of a path is expanded to your home directory.

---

## 9. Audio quality & formats

Set `quality` (or leave it at `max`):

| `quality`  | Tidal level       | Result                                    |
| ---------- | ----------------- | ----------------------------------------- |
| `low`      | LOW               | Lossy AAC                                 |
| `normal`   | HIGH              | Lossy AAC (higher bitrate)                |
| `high`     | LOSSLESS          | Lossless FLAC (16-bit/44.1kHz)            |
| `max`      | HI_RES_LOSSLESS   | Lossless FLAC, up to 24-bit/192kHz        |

Whatever the source, output is delivered as follows:

- **Lossless FLAC → ALAC** (`.m4a`), with embedded art and tags. This is the
  normal case.
- **Stereo, lossy source (AAC)** → passed through as `.m4a` (no pointless
  re-encoding).
- **Dolby Atmos only** (an album with *no* stereo master at all) → delivered as
  a lossy `.mp4` (eac3 5.1). `tidlr` requests the stereo stream by default, so
  this is rare — most "Atmos" albums do have a lossless stereo master, which
  `tidlr` finds and downloads as ALAC.

---

## 10. Troubleshooting

**`tidlr: command not found`**
Add Go's bin to your `PATH` (see [Installation](#1-installation)).

**`Tidal auth: … (run tidlr login)`**
You're not logged in, or the token file is missing. Run `tidlr login`.

**A download failed once, then worked on retry**
Transient — usually a dropped connection (e.g. a firewall prompt). `tidlr` retries
segments automatically; for whole albums, just run the command again or use
`tidlr retry` for queued items.

**Firewall keeps prompting after a rebuild**
macOS re-prompts to allow network access for a newly built binary. Approve it
once; subsequent runs of the same binary won't prompt.

**An album came out as `.mp4` instead of `.m4a`**
That album has no lossless stereo master on Tidal — only Dolby Atmos — so it was
delivered as the lossy Atmos fallback. This is expected and rare.

**`ffmpeg` errors during conversion**
Ensure `ffmpeg` is installed and on your `PATH` (`which ffmpeg`), or set
`ffmpeg_bin` in `config.toml` to its full path.

**Nothing new gets enqueued on `sync`**
You already have everything ADM currently lists. Use `--since <date>` to reach
further back (within ADM's ~6-week window), or download specific albums directly
with `--album`.
