# Changelog

All notable changes to `tidlr` are documented here. This project adheres to
[Semantic Versioning](https://semver.org).

## [1.6.1] — 2026-09-15

### Fixed

- **`keep_flac = true` now delivers FLACs to the output library.** The flag
  only guarded the `os.Remove` of each source file, so the FLACs were left in
  the scratch directory and never copied into the library — and the scratch
  directory is wiped right after conversion, so they were deleted anyway. The
  library received ALAC only, whatever the setting. Source `.flac` files are
  now copied into the output album directory alongside the transcoded `.m4a`.
- **`cover.jpg` is now delivered alongside the audio** when `keep_flac` is
  true. It was read as a transcode input for embedding art, then dropped by
  the `default` branch of the extension switch, so it never reached the
  library. Players and car head units that need artwork as a separate file
  now get it. Delivery happens outside the per-file loop, so the cover is
  never counted as a delivered track nor removed as a consumed source.
  (Thanks @starmanager01 — #4.)

## [1.6.0] — 2026-09-14

### Fixed

- **`--album` and `--track` now accept repeated flags.** Both were plain
  string flags, so `tidlr --album A --album B` silently kept only the last
  value and downloaded B alone. They now accumulate every occurrence; the
  collected values feed the existing multi-id parsers, which already split
  on spaces and commas, so `--album A --album B`, `--album "A B"` and
  `--album A,B` are all equivalent. Batch handling itself was already in
  place — each album is downloaded in turn, and one failure doesn't abort
  the rest.

## [1.5.0] — 2026-09-01

### Added

- **Multi-line per-track + album progress for `--album` downloads.** Live
  display now shows one row per concurrently-downloading track (title and
  segment progress bar, up to `Threads` rows) plus a trailing album-wide
  "N/M tracks" bar, redrawn in place via ANSI cursor movement. Falls back to
  a no-op on non-TTY output (redirected/CI logs), since there's no cursor to
  rewind there.
- `Downloader.OnAlbumStart(totalTracks int)`, called once the track list is
  fetched, so callers can size an album-wide progress display before any
  track starts downloading.

Playlist and `--track` downloads are unchanged (still the single shared
spinner) — there's no natural "album" grouping for those.

## [1.4.0] — 2026-08-28

### Added

- **Live progress bar for track downloads.** `Downloader` reports
  per-segment progress via a new `OnTrackStart` callback; the CLI renders it
  as a shared spinning progress bar across concurrent track downloads
  (`--album`, `--playlist`, `--track`).

### Changed

- `tidlr sync` now requeues failed items before scraping, matching `retry`'s
  existing behaviour — previously only `retry` did this.

## [1.3.0] — 2026-07-31

### Added

- **`skipped` queue state** for permanent, non-retryable outcomes. When no
  Tidal album matches an ADM title (typically because ADM's title is garbled),
  the item is now marked `skipped` rather than `failed`.

### Fixed

- `tidlr retry` no longer churns on unmatchable albums. The pipeline already
  recognised the no-match case and logged "retry won't help", but still
  recorded it as `failed` — so every `retry` requeued it, re-ran the download,
  and failed again. `RequeueFailed` targets `failed` only, so `skipped` items
  are now left alone permanently.
- `tidlr status` lists the new state. The displayed total is the sum of the
  listed states, so omitting it would have silently dropped skipped items from
  the count.

## [1.2.0] — 2026-07-31

### Added

- **`--track <url|id ...>`** — download individual tracks, the one thing
  `--album` and `--playlist` could not do. Accepts multiple space/comma-
  separated Tidal track URLs or bare ids (including the `/u` share-link
  suffix), and delivers ALAC into `<output_dir>/tracks/`.
- `Client.GetTrack` fetches a single track's metadata. Each track carries its
  own album, artist and cover, so standalone tracks are tagged and given
  embedded art individually — the playlist model rather than the album one.
- Tracks are named `<Artist> - <Title>`. A loose track has no album directory
  to sensibly live in, and filing it under `<Artist>/<Album>/` would leave
  album folders that look complete but aren't.
- One failed track is logged and skipped rather than aborting the batch,
  matching the existing `--album` behaviour.
- Unit tests for track and album URL/id parsing (`cmd/tidlr` had none).

## [1.1.0] — 2026-07-25

### Added

- **Rate-limit backoff.** Tidal throttles bursty API traffic with HTTP 429,
  which previously aborted the whole album mid-download (most often on
  `playbackinfopostpaywall` when several albums downloaded concurrently).
  Requests now retry up to 5 times, waiting a random 1–180s between attempts.
  The wait is randomised across the full window rather than escalating, so
  concurrent workers throttled at the same moment don't retry in lockstep and
  immediately re-trip the limit.
- A server-sent `Retry-After` header (delta-seconds or HTTP date) takes
  precedence over the random wait, clamped to 180s so an outsized value can't
  stall a run.
- Rate-limit waits are logged, so a multi-minute pause isn't mistaken for a
  hang, and remain cancellable — Ctrl-C returns promptly instead of blocking
  for the full backoff.

### Fixed

- Segment and cover downloads, which bypass the API client, now use the same
  backoff on a 429. Their existing retry waited only 1–4s — far too short to
  clear a rate limit — while keeping that short linear backoff for dropped
  connections.

## [1.0.0] — 2026-07-13

**Complete rewrite.** v1.0.0 is a ground-up reimplementation of `tidlr`. Nothing
is shared with the 0.9.x line — the codebase, architecture, and feature set are
entirely new. The pre-1.0 code is preserved on the [`legacy-0.9`] branch.

### Highlights

- **Fully native Go.** All Tidal integration — OAuth device-flow login, token
  refresh, the REST API, edition matching, and downloading — is implemented in
  Go. No Python, no bundled third-party downloader, no vendored token scraping.
  The only runtime dependency is `ffmpeg` (for the FLAC→ALAC conversion).
- **Lossless ALAC output.** Downloads FLAC from Tidal and converts to ALAC
  (`.m4a`) with embedded cover art and full metadata, filed under
  `<Artist>/<Album>/`.
- **Smart lossless-edition matching.** When resolving an album by name, `tidlr`
  searches Tidal and then scans the artist's catalogue to pick the best edition
  (lossless over Atmos, explicit original over clean, newest over oldest),
  surfacing lossless masters that Tidal's own search hides.
- **Stereo-first.** Requests the stereo stream explicitly, so albums that default
  to Dolby Atmos still download as lossless stereo where a master exists, rather
  than lossy 5.1.

### Added

- `tidlr login` — Tidal device-authorization flow (reuses an existing token if
  present).
- `tidlr --album <url|id ...>` — download one or more albums by URL/ID; a batch
  continues past individual failures.
- `tidlr --playlist <url|uuid>` — download a playlist into
  `playlist/<name>/`, preserving order and per-track album art.
- `tidlr sync` / `scrape` / `run` / `retry` / `status` — build and maintain a
  library from [AnyDecentMusic](http://www.anydecentmusic.com)'s release chart,
  with a persistent dedupe library and a resumable SQLite work-queue.
- `--since <DDMMYY>` to scrape releases back to a date; `--force` to re-download.
- Concurrent, resilient downloads with per-segment retries; interrupt and re-run
  to resume.
- Embedded cover art and title/artist/album/track/date tags on every track.
- README and a full [User Guide](docs/USER_GUIDE.md).

### Changed

- Distributed as a single Go binary via `go install ./cmd/tidlr` — no runtime
  besides `ffmpeg`.
- Default branch renamed `master` → `main`.

### Removed

- The entire 0.9.x implementation, including its vendored dependencies and the
  external token-scraping mechanism (Tidal auth is now handled natively).

---

## Pre-1.0 history (0.9.x)

The following predate the rewrite and describe the original utility, retained
here for continuity. See the [`legacy-0.9`] branch for that code.

### [0.9.4]

- Online update of access tokens.

### [0.9.3]

- Rotated access tokens after a Tidal change.

### [0.9.2] / [0.9.1]

- Early releases of the original Golang Tidal FLAC/MQA downloader.

[1.6.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.6.0
[1.5.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.5.0
[1.4.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.4.0
[1.3.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.3.0
[1.2.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.2.0
[1.1.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.1.0
[1.0.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.0.0
[0.9.4]: https://github.com/spbkaizo/tidlr/tree/legacy-0.9
[0.9.3]: https://github.com/spbkaizo/tidlr/releases/tag/v0.9.3
[0.9.2]: https://github.com/spbkaizo/tidlr/releases/tag/0.9.2
[0.9.1]: https://github.com/spbkaizo/tidlr/releases/tag/0.9.1
[`legacy-0.9`]: https://github.com/spbkaizo/tidlr/tree/legacy-0.9
