# Builds the dross binary with the Flutter web client embedded (go:embed of
# internal/web/dist), so one process serves the UI + JSON/SSE API. Run
# `flutter build web` (mobile/) before building this image — the prebuilt output
# is copied into the embed dir so no Flutter SDK is needed here.
FROM golang:1.26.5-alpine AS builder
WORKDIR /app
COPY . .

# The commit, because .dockerignore excludes .git — deliberately, it is large
# and nothing here needs it — so the toolchain has no VCS information to stamp
# and `dross --version` in a pod would name a release and no build.
ARG REVISION=""

# Replace the committed placeholder with the real web build, then compile.
#
# The bundle is built outside the image so no Flutter SDK is needed here, which
# means a build can pair a month-old client with a current server and look
# entirely healthy — cp is happy either way. The published images are safe
# because CI runs `flutter build web --release` immediately before this; a local
# `docker build` embeds whatever is on disk, so run it there too.
#
# Checking that here by comparing mtimes does not work, which is worth writing
# down: BuildKit normalises timestamps in the build context, so every file
# arrives the same age and `find -newer` never matches. It was tried, it passed
# a deliberately stale bundle, and a guard that cannot fire is worse than none.
# Only the bundle's absence is detectable, so only that is checked.
RUN rm -rf internal/web/dist && cp -r mobile/build/web internal/web/dist \
    && if [ ! -f internal/web/dist/main.dart.js ]; then \
         echo "no web bundle to embed — run 'flutter build web --release' in mobile/" >&2; \
         exit 1; \
       fi \
    && CGO_ENABLED=0 GOOS=linux go build \
       -ldflags "-X github.com/dougkirkley/dross/internal/cli.revision=${REVISION}" \
       -o dross ./cmd/dross

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
# The binary is the entrypoint, not one subcommand of it: the same image also
# runs `dross operator`. Serving stays the default via CMD, which a deployment
# overrides by supplying its own args.
ENTRYPOINT ["/app/dross"]
CMD ["serve", "--addr", ":8080"]
