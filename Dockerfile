# Stage 1: Build frontend
FROM node:26-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
# --legacy-peer-deps: openapi-typescript@7 declares peer typescript@^5 but
# we run typescript@~6.0; recent npm tolerates this loosely, the older npm
# in node:22-alpine does not. The local dev install runs without the flag
# (npm 11+) and produces an identical tree.
RUN npm ci --legacy-peer-deps
COPY web/ ./
RUN npm run build

# Stage 2: Build backend
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/web/dist ./internal/api/static/
RUN CGO_ENABLED=0 go build -o /nexus ./cmd/nexus

# Stage 3: Runtime
FROM alpine:3.24
# tini is a minimal init that runs as PID 1: it reaps zombie processes and
# forwards signals to the app. Without it, /nexus runs as PID 1 and a Go
# binary doesn't reap orphaned children — e.g. a Docker HEALTHCHECK using
# BusyBox `wget https://…` forks an `ssl_client` TLS helper that orphans to
# PID 1, accumulating <defunct> processes (~1 per healthcheck interval).
RUN apk add --no-cache ca-certificates tini
COPY --from=backend-builder /nexus /nexus
EXPOSE 8080
# Liveness probe: hit /api/health (always 200 while the HTTP server serves).
# Deliberately NOT /api/health/ready — a container self-restart tied to a
# downstream dependency (OpenSearch) would kill a healthy app during a blip.
# busybox wget ships in alpine (no curl); tini reaps the wget child.
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
	CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["/sbin/tini", "--", "/nexus"]
