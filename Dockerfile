# Multi-stage Dockerfile for Google Cloud Run
# Stage 1: Build binary with pure Go SQLite (CGO_ENABLED=0)
FROM golang:alpine AS builder

WORKDIR /src

# Install build tools if needed
RUN apk add --no-cache git ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary for Linux AMD64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bin/server ./cmd/server

# Stage 2: Minimal runtime image
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

# Create dedicated non-root user and persistent data folder
RUN adduser -D -u 10001 appuser && \
    mkdir -p /data /app && \
    chown -R appuser:appuser /data /app

WORKDIR /app

# Copy binary and static web assets
COPY --from=builder /bin/server /app/server
COPY --from=builder /src/web/static /app/web/static

USER appuser

# Environment variables for Cloud Run
ENV PORT=8080 \
    DB_PATH=/tmp/app.db \
    GEMINI_LIVE_MODEL=auto

EXPOSE 8080

ENTRYPOINT ["/app/server"]
