# Multi-stage Dockerfile for Auth Service in Go Multi-Module Workspace

# Stage 1: Build binary
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install git, ca-certificates, and build utilities
RUN apk add --no-cache git ca-certificates tzdata

# Create non-root unprivileged app user in build stage
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy Go workspace definitions and module dependencies first (for layer caching)
COPY go.work go.work.sum ./
COPY platform/go.mod platform/go.sum* ./platform/
COPY services/auth/go.mod services/auth/go.sum* ./services/auth/

# Download dependencies
RUN go mod download

# Copy application source code
COPY platform/ ./platform/
COPY services/auth/ ./services/auth/

# Build statically-linked Linux binary without CGO, omitting symbol table and debug information
WORKDIR /app/services/auth
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -extldflags '-static'" \
    -o /bin/auth-server ./cmd/server

# Stage 2: Minimal hardened runtime image
FROM alpine:3.19

# Install security certificates and timezone definitions
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy system user and group from builder
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Copy compiled binary from builder
COPY --from=builder /bin/auth-server /app/auth-server

# Enforce non-root execution
USER appuser:appgroup

EXPOSE 8081

# Container healthcheck: executes the binary's built-in probe against /healthz
# Returns non-zero exit code if unhealthy, without suppressing failure
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/app/auth-server", "-healthcheck"]

ENTRYPOINT ["/app/auth-server"]
