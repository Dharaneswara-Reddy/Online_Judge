# Production image for the CodeArena API and judge worker.
#
# One image builds both binaries; the compose file picks which to run.
# The build stage pins the toolchain the project declares, so a host with
# GOTOOLCHAIN=local cannot silently produce an older build.
FROM golang:1.25 AS build
WORKDIR /src

# Dependencies first, so a source-only change reuses the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static binaries: the runtime stage has no libc.
ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
RUN go build -trimpath -ldflags="-s -w" -o /out/api    ./cmd/api && \
    go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    go build -trimpath -ldflags="-s -w" -o /out/seed   ./cmd/seed

# Runtime. Alpine rather than scratch because the worker shells out to
# nothing but does need CA certificates for MongoDB Atlas over TLS.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 codearena
COPY --from=build /out/api /out/worker /out/seed /usr/local/bin/

# The API runs unprivileged. The worker overrides this in compose because
# it needs the docker group to reach the daemon socket.
USER codearena
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
