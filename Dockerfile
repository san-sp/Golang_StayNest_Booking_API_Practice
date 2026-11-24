# Build stage
FROM golang:1.27.1-alpine AS builder

WORKDIR /app

# Download dependencies first for better build caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy application source code.
COPY . .

# Build the Go application.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /staynest-api ./cmd/api

# Build health-check executable.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /healthcheck ./cmd/healthcheck

# Runtime stage
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder --chown=app:app /staynest-api /app/staynest-api
COPY --from=builder --chown=app:app /healthcheck /app/healthcheck

USER app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/healthcheck"]

ENTRYPOINT ["/app/staynest-api"]
