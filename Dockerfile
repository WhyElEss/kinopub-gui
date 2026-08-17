# Multi-stage build for a headless (server) install: the React UI is compiled,
# embedded into a static Go binary, and shipped with ffmpeg.
#
# The image builds for whatever platform Docker targets — on the Raspberry Pi
# that is linux/arm64 natively, so no emulation is involved.

FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
# npm ci needs a lockfile; fall back to install when the repo has none.
RUN npm ci 2>/dev/null || npm install
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overwrite the committed dist with the one just built from these sources.
RUN rm -rf web/dist
COPY --from=web /web/dist ./web/dist
ARG VERSION=docker
# CGO off keeps the binary static and selects the headless lifecycle (no tray).
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -trimpath \
    -o /out/kinopub-gui ./cmd/kinopub-gui

FROM alpine:3.21
# ffmpeg/ffprobe do the muxing; ca-certificates for TLS to kino.pub; tzdata so
# TZ= gives correct timestamps in the log.
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=build /out/kinopub-gui /usr/local/bin/kinopub-gui

# Settings and the encrypted credentials live here (XDG_CONFIG_HOME/kinopub).
ENV XDG_CONFIG_HOME=/config
VOLUME ["/config"]
EXPOSE 8765

# -lan is what makes the server answer requests addressed to the host's LAN IP;
# -no-self-update keeps the in-app updater from replacing the binary inside an
# image that would be restored on the next restart anyway.
ENTRYPOINT ["/usr/local/bin/kinopub-gui"]
CMD ["-addr", "0.0.0.0:8765", "-lan", "-no-open", "-no-self-update"]
