# Builds the dross binary with the Flutter web client embedded (go:embed of
# internal/web/dist), so one process serves the UI + JSON/SSE API. Run
# `flutter build web` (mobile/) before building this image — the prebuilt output
# is copied into the embed dir so no Flutter SDK is needed here.
FROM golang:1.26.5-alpine AS builder
WORKDIR /app
COPY . .
# Replace the committed placeholder with the real web build, then compile.
RUN rm -rf internal/web/dist && cp -r mobile/build/web internal/web/dist \
    && CGO_ENABLED=0 GOOS=linux go build -o dross ./cmd/dross

FROM alpine:latest
# Sandboxed Code sessions run shell commands via the bash tool, so the runtime
# needs bash (alpine ships only /bin/sh) plus git and certs for real work.
RUN apk add --no-cache bash git ca-certificates curl xz
# Preinstall the Go toolchain (matches the builder) so sandboxed Code sessions
# have `go` on PATH without downloading it. GOPATH/GOBIN/GOCACHE are redirected
# into each session's workspace by the sandbox, so installs still stay scoped.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"
WORKDIR /app
COPY --from=builder /app/dross .

# Run as a normal user. The agent executes model-chosen shell commands, so root
# in the container is one container-escape away from being root on the node —
# and it makes the sudo guard load-bearing instead of belt-and-braces. Writable
# state (workspace, artifacts, data, /tmp) comes from mounted volumes, so the
# root filesystem can stay read-only.
RUN addgroup -g 10001 dross && adduser -D -u 10001 -G dross dross \
    && chown -R dross:dross /app
USER 10001:10001
EXPOSE 8080
# serve binds all interfaces so the gateway can reach it; hosts UI + API.
ENTRYPOINT ["./dross", "serve", "--addr", ":8080"]
