# syntax=docker/dockerfile:1

# Stage 1: build the static bot binary.
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Copy module manifests first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source.
COPY cmd ./cmd
COPY internal ./internal

# Build a static binary so it can run on a minimal runtime image.
# CGO is disabled because the production binary has no cgo dependencies
# (sqlite is only used by tests).
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/bot ./cmd/bot

# Stage 2: minimal runtime image.
FROM alpine:3.20

# ca-certificates + tzdata are needed for HTTPS (Discord API) and TZ handling.
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /out/bot /app/bot

# Run as a non-root user.
RUN adduser -D -u 1000 botuser && chown botuser /app
USER botuser

# The bot only makes outbound WebSocket/HTTPS connections to Discord, so no
# EXPOSE is required.
CMD ["/app/bot"]
