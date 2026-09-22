# syntax=docker/dockerfile:1

# --- build ---
FROM golang:1.26 AS builder
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 is a hard project constraint (see CLAUDE.md) — modernc.org/sqlite
# is pure Go specifically so this works. -trimpath/-ldflags="-s -w" trim
# local paths and debug symbols for a smaller, more reproducible binary.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/permeable ./cmd/permeable
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck

# --- runtime ---
# distroless/static, not scratch: scratch has no CA trust store, which
# breaks HTTPS fetches of url-type sources (e.g. Google Calendar). No
# shell here either — see cmd/healthcheck for why HEALTHCHECK below is a
# real binary, not a `curl`/`wget` one-liner.
FROM gcr.io/distroless/static-debian12

COPY --from=builder /out/permeable /permeable
COPY --from=builder /out/healthcheck /healthcheck

# SQLite file + generated feed live here; mount a volume in production
# so they survive container recreation (see CLAUDE.md "Repository layout").
VOLUME /data

# Admin UI (8080) and feed (8081) — see CLAUDE.md "Endpoints" for the
# split rationale. Only publish the feed port to the internet; keep the
# admin port on a LAN-only network/bind.
EXPOSE 8080 8081

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/healthcheck"]

ENTRYPOINT ["/permeable"]
CMD ["-db", "/data/permeable.db"]
