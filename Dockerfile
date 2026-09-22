# --- Build stage ---
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src/server

# Cache dependencies
COPY server/go.mod server/go.sum ./
RUN go mod download

# Copy server source
COPY server/ ./

# Build binaries
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o bin/server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o bin/multica ./cmd/multica
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/maintenance ./cmd/maintenance
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/backfill_task_usage_hourly ./cmd/backfill_task_usage_hourly
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/backfill_codex_usage_cache ./cmd/backfill_codex_usage_cache

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

RUN addgroup -g 10001 -S multica && \
    adduser -u 10001 -S multica -G multica && \
    mkdir -p /app/data/uploads && \
    chown -R multica:multica /app

COPY --from=builder --chown=multica:multica /src/server/bin/server .
COPY --from=builder --chown=multica:multica /src/server/bin/multica .
COPY --from=builder --chown=multica:multica /src/server/bin/migrate .
COPY --from=builder --chown=multica:multica /src/server/bin/maintenance .
COPY --from=builder --chown=multica:multica /src/server/bin/backfill_task_usage_hourly .
COPY --from=builder --chown=multica:multica /src/server/bin/backfill_codex_usage_cache .
COPY --chown=multica:multica server/migrations/ ./migrations/
COPY --chown=multica:multica LICENSE NOTICE ./
COPY --chown=multica:multica docker/entrypoint.sh .
RUN sed -i 's/\r$//' entrypoint.sh && chmod +x entrypoint.sh

USER multica

EXPOSE 8080

ENTRYPOINT ["./entrypoint.sh"]
