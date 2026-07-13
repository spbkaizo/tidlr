# tidlr — build plan

A speed-focused music pipeline: scrape new albums from AnyDecentMusic, download lossless
from Tidal, convert FLAC→ALAC, hand off to MusicBrainz Picard.

## Architecture (decided)

**Go orchestrator + Python downloader.** Go owns the pipeline and concurrency; it shells
out to the Python `tidal-dl-ng` CLI (in an isolated venv) only for the Tidal fetch. The
downloader lives behind a Go `Downloader` interface so it can be swapped when the upstream
tool inevitably churns.

```
  ADM scrape ──► queue (dedupe) ──► [worker pool] ──► Tidal download ──► FLAC→ALAC ──► Picard drop dir
   (Go)          (Go, on disk)       (Go)              (Python CLI)       (Go+ffmpeg)    (Go move)
```

Queue policy: enqueue **everything new** on ADM's `JustOut.aspx` ("Just in"), dedupe by
ADM review id. Curation happens later in Picard.

## Verified facts
- ffmpeg 8.1 present; `ffmpeg -i in.flac -c:a alac out.m4a` produces genuine `Audio: alac`. ✔
- ADM `/chart/JustOut.aspx` lists releases as `dl.sidebar_list`: artist in `<dt><strong><a>`,
  album title in the following `<a>`, review link `/review/<id>/Artist-Title.aspx`. ✔
- Go 1.26.5, Python 3.14.6 available. Picard NOT yet installed (install step below). 
- `tidal-dl-ng` exact repo/CLI to be pinned at build time (README 404'd during planning);
  isolated behind the interface so this is low-risk.

## Layout
```
tidlr/
  go.mod
  cmd/tidlr/main.go            # CLI: `tidlr scrape`, `tidlr run`, `tidlr status`
  internal/adm/                # ADM scraper (net/http + goquery), returns []Release
  internal/queue/             # on-disk queue: JSON/SQLite, states pending→downloading→converting→done→failed
  internal/downloader/        # Downloader interface + tidaldlng implementation (exec.Command)
  internal/convert/           # ffmpeg FLAC→ALAC, preserves tags, parallel
  internal/picard/            # move/copy finished ALAC into Picard watch/drop folder
  internal/pipeline/          # worker pool wiring stages together with bounded concurrency
  config.toml                 # paths, concurrency, Tidal quality, Picard drop dir
  python/                     # isolated venv bootstrap for tidal-dl-ng (setup script)
```

## Build steps
1. **Scaffold**: `go mod init`, config loading (TOML), directory + config skeleton.
2. **ADM scraper**: fetch JustOut, parse releases, return `{ReviewID, Artist, Album, URL}`.
   Unit-test against a saved HTML fixture so we don't hammer the site.
3. **Queue**: persistent store (SQLite via modernc.org/sqlite — pure Go, no cgo). Dedupe by
   ReviewID. Idempotent enqueue. Track per-item state + error for retry.
4. **Downloader interface + tidal-dl-ng bootstrap**: pin the tool, script the venv, wire
   login (device-code, one-time), implement `Download(ctx, Release) -> localFLACdir`.
   Configure max quality (HiRes/MAX lossless).
5. **Converter**: for each downloaded FLAC, `ffmpeg -c:a alac`, carry metadata + cover art,
   run N in parallel. Verify output codec.
6. **Picard handoff**: move finished ALAC album folders into Picard's watch dir. (Install
   Picard; decide on its "add folder"/watch config — may need a small nudge or its own CLI.)
7. **Pipeline**: bounded worker pools per stage (download concurrency is the Tidal-rate-limit
   bottleneck; conversion is CPU-bound — separate limits), resumable from queue state.
8. **CLI + a `run` loop** we can cron/loop to keep catching up.

## Open items to resolve during build
- Pin the exact `tidal-dl-ng` package/version and its login + album-URL invocation.
- We download from Tidal by artist/album *name* from ADM — need a Tidal **search→match** step
  (ADM gives us names, not Tidal IDs). Matching quality matters; may add a confirm/skip on
  low-confidence matches.
- Picard automation: watch-folder vs. scripted run; where the final music library lives.

## First increment to build now
Steps 1–3 (scaffold + ADM scraper with fixture test + persistent dedupe queue). This is the
whole Go-side foundation and needs no Tidal account, so it's safe to stand up and verify
immediately. Then tackle the Tidal downloader (step 4), which needs your Tidal login.
