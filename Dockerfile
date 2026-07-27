# Builds the holocron binary with the Flutter web client embedded (go:embed of
# internal/web/dist), so one process serves the UI + JSON/SSE API. Run
# `flutter build web` (mobile/) before building this image — the prebuilt output
# is copied into the embed dir so no Flutter SDK is needed here.
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY . .
# Replace the committed placeholder with the real web build, then compile.
RUN rm -rf internal/web/dist && cp -r mobile/build/web internal/web/dist \
    && CGO_ENABLED=0 GOOS=linux go build -o holocron ./cmd/holocron

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/holocron .
EXPOSE 8080
# serve binds all interfaces so the gateway can reach it; hosts UI + API.
ENTRYPOINT ["./holocron", "serve", "--addr", ":8080"]
