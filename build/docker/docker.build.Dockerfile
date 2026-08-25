# Multi-stage build for Tingly Box
# Stage 1: Build
FROM golang:1.26-alpine AS builder

# Install git, nodejs, npm, gcc (for CGO), and other build dependencies
RUN apk add --no-cache git nodejs npm ca-certificates tzdata curl jq gcc musl-dev

# Install Task (task runner)
RUN go install github.com/go-task/task/v3/cmd/task@latest

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Copy the entire source code (including submodule if initialized)
COPY . .

# Ensure openai-go submodule exists (clone if user hasn't initialized submodules)
RUN if [ ! -f libs/openai-go/go.mod ]; then \
      rm -rf libs/openai-go && \
      git clone -b fork --depth 1 https://github.com/tingly-dev/openai-go.git libs/openai-go; \
    fi

RUN if [ ! -f libs/anthropic-sdk-go/go.mod ]; then \
      rm -rf libs/anthropic-sdk-go && \
      git clone -b fork --depth 1 https://github.com/tingly-dev/anthropic-sdk-go.git libs/anthropic-sdk-go; \
    fi

RUN if [ ! -f libs/go-genai/go.mod ]; then \
      rm -rf libs/go-genai && \
      git clone -b main --depth 1 https://github.com/tingly-dev/go-genai.git libs/go-genai; \
    fi

# Download dependencies (must be after source copy due to local replace directive)
RUN go mod download

# Build the web UI before the binary. internal/server/webui_handler.go serves
# the dashboard from the embedded internal/web/dist; without this step the
# binary compiles fine and then returns HTTP 500 for every UI route.
# Mirrors Taskfile's web:build, minus the swagger regeneration — openapi.json
# is committed, so gen:api needs no Go tooling. pnpm is installed at the
# version package.json pins: a newer global pnpm self-provisions that pin and
# fails integrity, since @pnpm/exe.<platform> is absent from pnpm-lock.yaml.
RUN cd frontend && \
    npm install -g "pnpm@$(node -p "require('./package.json').packageManager.split('@')[1]")" && \
    pnpm install --no-frozen-lockfile && \
    pnpm gen:api && \
    pnpm build && \
    cd .. && \
    mkdir -p internal/web/dist && \
    cp -R frontend/dist/* internal/web/dist/

# Build with static linking for SQLite (musl)
RUN CGO_ENABLED=1 \
    go build \
    -tags 'sqlite_omit_load_extension' \
    -ldflags '-linkmode external -extldflags "-static"' \
    -o ./build/tingly-box ./cli/tingly-box

# Rename binary to expected name
RUN mv ./build/tingly-box ./tingly

# Stage 2: Runtime
FROM alpine:latest

# Install ca-certificates for HTTPS requests and su-exec for running as non-root
RUN apk --no-cache add ca-certificates su-exec tzdata

# Create app user
RUN addgroup -S tingly && \
    adduser -S -G tingly tingly

# Set the Current Working Directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/tingly /usr/local/bin/tingly

# Create necessary directories with proper permissions
RUN mkdir -p /home/tingly/.tingly-box /app/memory /app/logs && \
    chown -R tingly:tingly /app /home/tingly

# Switch to non-root user
USER tingly

# Expose port
EXPOSE 12580

# Environment variables for configuration
ENV TINGLY_PORT=12580
ENV TINGLY_HOST=0.0.0.0

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD tingly status || exit 1

# Default command (server mode)
CMD ["sh", "-c", "echo '======================================' && \
     echo '  Tingly Box is starting up...' && \
     echo '  Web UI will be available at:' && \
     echo '  http://localhost:'${TINGLY_PORT}'/dashboard?user_auth_token=tingly-box-user-token' && \
     echo '======================================' && \
     rm -f /home/tingly/.tingly-box/tingly-server.pid && \
     exec tingly start --host ${TINGLY_HOST} --port ${TINGLY_PORT}"]

# Volumes for persistent data
VOLUME ["/home/tingly/.tingly-box", "/app/memory", "/app/logs"]