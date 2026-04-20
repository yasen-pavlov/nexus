# Stage 1: Build frontend
FROM node:24-alpine AS frontend-builder
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
FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=backend-builder /nexus /nexus
EXPOSE 8080
ENTRYPOINT ["/nexus"]
