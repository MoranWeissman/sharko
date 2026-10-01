# Both build stages run on the build machine's own architecture
# (--platform=$BUILDPLATFORM), then cross-compile their output for the
# target architecture. This avoids QEMU emulation for the two heavy
# stages (npm install/build, go build) — only the small final alpine
# stage below still runs per-arch (its `apk add` is cheap even under
# emulation). BUILDPLATFORM/TARGETOS/TARGETARCH are buildx's automatic
# platform args; no need to declare BUILDPLATFORM, it's predefined.

# Stage 1: Build UI (output is a static bundle — arch-independent, so
# build it once natively regardless of the final image's target arch)
FROM --platform=$BUILDPLATFORM node:22-alpine AS ui-build
WORKDIR /app/ui
COPY ui/package*.json ./
RUN npm ci --legacy-peer-deps
COPY ui/ .
RUN npm run build

# Stage 2: Build Go binary, cross-compiled for the target arch
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS go-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY catalog/ catalog/
COPY templates/ templates/
COPY docs/swagger/ docs/swagger/
COPY version.txt ./
# ARG CACHE_BUST must appear after COPY and before go build so that:
# - COPY layers are still cached by content (faster builds when source unchanged)
# - go build layer is always invalidated when CACHE_BUST changes (set to git SHA in CI)
ARG CACHE_BUST=dev
ARG COMMIT=dev
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build \
	-ldflags "-X main.version=$(cat version.txt) -X main.commit=${COMMIT}" \
	-o sharko ./cmd/sharko

# Stage 3: Final image
FROM alpine:3.21
# OpenSSL floor: libcrypto3 and libssl3 must be 3.3.7-r2 or later, which
# fixes CVE-2026-75804 and CVE-2026-84782 (High). The alpine:3.21 base
# layer can still carry an older build, so apk is asked for the floor
# explicitly, and the loop below fails the build if what got installed is
# older. `--no-cache` only skips apk's index cache — Docker still reuses a
# cached RUN layer whose text is unchanged. Having the floor in the command
# text is what makes every older cached layer unusable. `min` is the one
# place to change the floor.
RUN min=3.3.7-r2 && \
    apk add --no-cache ca-certificates "libcrypto3>=$min" "libssl3>=$min" && \
    for p in libcrypto3 libssl3; do \
      v=$(awk -v p="$p" '/^P:/{n=substr($0,3)} /^V:/{if (n==p) print substr($0,3)}' /lib/apk/db/installed); \
      echo "$p installed: ${v:-none} (floor $min)"; \
      [ -n "$v" ] && [ "$(apk version -t "$v" "$min")" != "<" ] || { echo "$p is below $min" >&2; exit 1; }; \
    done && \
    mkdir -p /home/sharko/.sharko && \
    chown -R 1001:1001 /home/sharko && \
    chmod 700 /home/sharko/.sharko
COPY --from=go-build /app/sharko /usr/local/bin/
COPY --from=ui-build /app/ui/dist /app/static
ENV SHARKO_STATIC_DIR=/app/static
ENV SHARKO_PORT=8080
# HOME is set so the CLI (sharko login, sharko apply, etc.) can persist its
# config to ~/.sharko/config without needing an external -e HOME=... flag.
# os.UserHomeDir() in Go reads $HOME, which is otherwise empty under USER 1001.
ENV HOME=/home/sharko
EXPOSE 8080
USER 1001
ENTRYPOINT ["sharko", "serve"]
