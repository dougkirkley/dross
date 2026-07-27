FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod ./
# COPY go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o holocron cmd/holocron/main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/holocron .
# Configure the local storage directory mapping
VOLUME ["/root/.config/holocron/sessions", "/app/uploads"]
EXPOSE 8080
CMD ["./holocron"]
