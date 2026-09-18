# Local development build. Stamps the last released tag into the binary so
# `tidlr -v` can report which release a dev build is based on; the commit hash
# and date come from the VCS stamp the Go toolchain embeds automatically.
#
# Release binaries are built by .github/workflows/release.yml, which stamps
# main.version with the pushed tag instead.

BASE_VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null)

.PHONY: build test fmt vet clean

build:
	go build -ldflags "-X main.baseVersion=$(BASE_VERSION)" -o tidlr ./cmd/tidlr

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal

vet:
	go vet ./...

clean:
	rm -f tidlr
