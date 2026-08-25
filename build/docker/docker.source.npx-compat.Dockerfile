# Source-built image that is a drop-in replacement for the published npx image.
#
# docker.build.Dockerfile produces an Alpine runtime. Images that derive FROM
# the published tingly-box image assume the npx layout instead — Debian
# (apt-get), node/npm on PATH, the binary named `tingly-box`, and
# /app/.tingly-box as the config dir. Swapping an Alpine base under such a
# Dockerfile fails immediately on `apt-get: not found`.
#
# Stage 1 is the builder from docker.build.Dockerfile, unchanged. Stage 2 is
# the runtime from docker.npx.Dockerfile, with the compiled binary dropped in
# where `npm install -g tingly-box` would have put it.

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


# Stage 2: Runtime — mirrors docker.npx.Dockerfile so derived images build
FROM node:20-slim

EXPOSE 12580

ENV TINGLY_PORT=12580
ENV TINGLY_HOST=0.0.0.0

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for security
RUN groupadd -r tingly && \
    useradd -r -g tingly tingly

# update modules, spec version to confirm security
RUN npm install -g npm@10.8.2
RUN npm install -g pm2@7.0.1

# The binary is statically linked against musl in the builder, so it runs on
# glibc-based Debian unchanged. Named `tingly-box` because derived entrypoints
# invoke it by that name.
COPY --from=builder /app/build/tingly-box /usr/local/bin/tingly-box
RUN chmod 755 /usr/local/bin/tingly-box

# Grant tingly user access to npm global directories and cache
RUN chown -R tingly:tingly /usr/local/lib/node_modules /usr/local/bin /root/.npm

WORKDIR /app

RUN mkdir -p /app/.tingly-box /app/memory /app/logs && \
    chown -R tingly:tingly /app

RUN mkdir -p /home/tingly && chown -R tingly:tingly /home/tingly/

USER tingly

RUN tingly-box version

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD tingly-box version || exit 1

CMD ["sh", "-c", "echo '======================================' && \
    echo '  Tingly Box is starting up...' && \
    echo '  Web UI will be available at:' && \
    echo '  http://localhost:'${TINGLY_PORT}'/dashboard?user_auth_token=tingly-box-user-token' && \
    echo '======================================' && \
    pm2 start \"tingly-box restart --host ${TINGLY_HOST} --port ${TINGLY_PORT} ${TINGLY_DEBUG:+--verbose --debug}\" --name tingly-box && \
    exec pm2 logs --raw"]

VOLUME ["/app/.tingly-box", "/app/memory", "/app/logs"]
