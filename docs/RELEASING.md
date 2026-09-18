# Releasing tidlr

How to get a change from a working tree onto `main` and out as a tagged
release. Written down because two details bite: `main` requires a pull request,
and release binaries only reproduce with `CGO_ENABLED=0`.

---

## Contents

1. [Branch protection: what is enforced](#1-branch-protection-what-is-enforced)
2. [Landing a change on `main`](#2-landing-a-change-on-main)
3. [Cutting a release](#3-cutting-a-release)
4. [Verifying a published binary](#4-verifying-a-published-binary)
5. [Version numbering](#5-version-numbering)
6. [Build stamping: how `-v` gets its values](#6-build-stamping-how--v-gets-its-values)

---

## 1. Branch protection: what is enforced

`main` is protected. The rules:

| Rule | Setting |
| ---- | ------- |
| Pull request required | yes, **0 approvals** (you can self-merge) |
| Required status check | `Build & test` (the CI job name) |
| Strict | yes — the branch must be up to date with `main` before merging |
| Conversation resolution | required |
| Force pushes / deletions | blocked |
| Admin enforcement | **off** |

**Admin enforcement is off, which means these rules do not stop you.** A direct
`git push origin main` from the repo admin succeeds; GitHub prints the rule as
a warning and lets the push through:

```
remote: - Changes must be made through a pull request.
remote: - Required status check "Build & test" is expected.
   1156fbd..74aa7b0  main -> main          <- it still pushed
```

Treat that output as a failed push and it will mislead you: the commit is on
`main`. Either follow the PR flow below, or accept that a direct push lands
without CI having run first.

To make the rules binding on everyone including admins:

```sh
gh api -X PUT repos/spbkaizo/tidlr/branches/main/protection/enforce_admins
```

(and `-X DELETE` on the same path to turn it back off).

---

## 2. Landing a change on `main`

```sh
# 1. Branch
git checkout -b feat/my-change

# 2. Commit
git add -A
git commit            # see the message convention below

# 3. Push and open a PR
git push -u origin feat/my-change
gh pr create --fill

# 4. Wait for "Build & test", then merge
gh pr checks --watch
gh pr merge --squash --delete-branch

# 5. Sync local main
git checkout main && git pull
```

Before pushing, run what CI runs — it is faster to find a failure locally:

```sh
gofmt -l ./cmd ./internal    # must print nothing
go vet ./...
go test ./...
go test -race ./...          # the progress display is concurrent; worth it
```

> `internal/tidal/matcher.go` is not gofmt-clean and predates this doc. It will
> show up in `gofmt -l` output. Leave it unless you are deliberately fixing it,
> so the reformat does not ride along in an unrelated diff.

**Commit messages** follow the existing history: a `type: summary` subject
(`feat:`, `fix:`, `chore:`, `docs:`, `ci:`), then a body explaining *why*. The
changelog is written by hand, not generated, so the commit body is free to
carry reasoning the changelog does not.

---

## 3. Cutting a release

Releases are built by `.github/workflows/release.yml`, which triggers on any
tag matching `v*`. Pushing the tag is the whole release.

```sh
# 1. Be on an up-to-date, clean main
git checkout main && git pull
git status --short          # must be empty

# 2. Set the version and date in CHANGELOG.md
#    Change "## [Unreleased]" to "## [1.9.0] — 2026-09-18".
#    Land that edit via a PR like any other change (step 2 above).

# 3. Tag the merged commit, annotated, with a summary body
git tag -a v1.9.0 -m "v1.9.0

Added
- ...

Fixed
- ..."

# 4. Push the tag — this is what publishes
git push origin v1.9.0

# 5. Watch it build (6 platforms, ~1 minute)
gh run watch "$(gh run list --workflow=Release --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

The workflow cross-compiles `darwin/{arm64,amd64}`, `linux/{amd64,arm64}`,
`windows/amd64` and `freebsd/amd64`, writes a `.sha256` beside each binary, and
publishes them all to a GitHub Release named after the tag.

Check the result:

```sh
gh release view v1.9.0 --json tagName,url,assets --jq '.tagName, .url, (.assets[].name)'
```

### If a release goes wrong

Tags are cheap; a bad release is best replaced by the next patch version rather
than rewritten. Deleting a published tag breaks anyone who already fetched it.
If nothing has consumed it yet:

```sh
gh release delete v1.9.0 --yes
git push --delete origin v1.9.0
git tag -d v1.9.0
```

---

## 4. Verifying a published binary

Release builds set `CGO_ENABLED=0`. **A local build without that flag produces a
functionally identical binary with a different SHA256**, so a checksum
comparison fails for a reason that has nothing to do with the code. Most shells
default to `CGO_ENABLED=1` — check yours with `go env CGO_ENABLED`.

The driver (`modernc.org/sqlite`) is pure Go, so cgo is never actually needed.

To reproduce a published binary exactly:

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=v1.9.0" \
  -o tidlr ./cmd/tidlr

shasum -a 256 tidlr
# compare against the published tidlr-<os>-<arch>.sha256 asset
```

To install a release version locally, use exactly that command with
`-o "$(go env GOPATH)/bin/tidlr"`.

---

## 5. Version numbering

[Semantic versioning](https://semver.org):

- **Patch** (`v1.9.1`) — bug fixes, no new flags or config, no behaviour change.
- **Minor** (`v1.10.0`) — new features or config options; existing invocations
  keep working.
- **Major** (`v2.0.0`) — a flag, config key, or default changes such that an
  existing setup behaves differently in a way the user must act on.

A changed *default* that stays config-reversible has been treated as a minor
bump: v1.9.0 made `sync` download albums serially by default, reversible with
`progress = false`.

---

## 6. Build stamping: how `-v` gets its values

`tidlr -v` prints three lines, from three different sources:

```
tidlr v1.9.0                    <- ldflags, or "dev (based on <tag>)"
  commit:  1156fbd (2026-09-18) <- embedded VCS stamp, automatic
  go:      go1.27.1 darwin/arm64
```

- **Release builds** set `-X main.version=<tag>`; the workflow does this from
  `GITHUB_REF_NAME`.
- **Dev builds** leave it empty and report `dev`, plus `-X main.baseVersion` for
  the "based on" clause. `make build` fills that from
  `git describe --tags --abbrev=0`. A plain `go build` works and just omits the
  clause.
- **The commit line** comes from the build info the Go toolchain embeds — no
  flags, and it cannot drift from the source. The date shown is the *commit*
  time, not the build time, so rebuilding a tree always reports the same
  version. An uncommitted change marks it `-dirty`.

`release.yml` checks out with `fetch-depth: 0` so tags and full history are
available to the build.
