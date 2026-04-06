# Stage 1: Build UI
FROM node:22-alpine AS ui-build
WORKDIR /app/ui
COPY ui/package*.json ./
RUN npm ci --legacy-peer-deps
COPY ui/ .
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.25.8-alpine AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# CACHE_BUST invalidates the layer cache when source changes (set to git SHA in CI)
ARG CACHE_BUST=dev
ARG VERSION=dev
COPY cmd/ cmd/
COPY internal/ internal/
COPY templates/ templates/
COPY docs/swagger/ docs/swagger/
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o sharko ./cmd/sharko

# Stage 3: Final image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=go-build /app/sharko /usr/local/bin/
COPY --from=ui-build /app/ui/dist /app/static
COPY version.txt /app/version.txt
ENV SHARKO_STATIC_DIR=/app/static
ENV SHARKO_PORT=8080
EXPOSE 8080
USER 1001
ENTRYPOINT ["sharko", "serve"]
