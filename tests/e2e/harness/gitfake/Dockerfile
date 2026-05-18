# gitfake-server image — standalone HTTP wrapper around the in-memory git
# harness (tests/e2e/harness/gitfake_core.go). Deployed as a Pod inside
# kind clusters by downstream Story 13.x.2 so the Sharko pod can clone /
# push against an in-cluster git endpoint that the git-host allowlist can
# permit.
#
# Build context must be the repo root (Dockerfile copies go.mod + the
# full tree). Build via the Makefile target `make build-gitfake-image`
# which mirrors the Sharko image-cache probe pattern.

# Stage 1: build the binary against the pinned Go toolchain.
FROM golang:1.25.8-alpine AS builder
WORKDIR /src

# Cache go.mod/go.sum download layer separately from source so source-only
# edits don't re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

# Copy only what the binary needs: the harness package + cmd dir. We
# deliberately do NOT copy the whole repo to keep build context tight.
# The harness package compiles without the e2e build tag (gitfake_core.go
# is the only file without the tag), which is exactly what the binary
# imports.
COPY tests/ tests/

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/gitfake-server \
    ./tests/e2e/harness/gitfake/cmd/gitfake-server

# Stage 2: distroless runtime — minimal attack surface, no shell, no apk.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/gitfake-server /gitfake-server

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gitfake-server"]
