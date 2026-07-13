# Changelog

All notable changes to `tidlr` are documented here. This project adheres to
[Semantic Versioning](https://semver.org).

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

[1.0.0]: https://github.com/spbkaizo/tidlr/releases/tag/v1.0.0
[0.9.4]: https://github.com/spbkaizo/tidlr/tree/legacy-0.9
[0.9.3]: https://github.com/spbkaizo/tidlr/releases/tag/v0.9.3
[0.9.2]: https://github.com/spbkaizo/tidlr/releases/tag/0.9.2
[0.9.1]: https://github.com/spbkaizo/tidlr/releases/tag/0.9.1
[`legacy-0.9`]: https://github.com/spbkaizo/tidlr/tree/legacy-0.9
