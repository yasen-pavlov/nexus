#!/usr/bin/env bash
# Generate TypeScript types from the Go backend's swagger spec.
#
# The backend uses swaggo/swag v1 which outputs Swagger 2.0. We convert that
# to OpenAPI 3.0 with swagger2openapi, then feed it to openapi-typescript.
#
# Regenerate after any `make swagger` change on the backend.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
SWAGGER="$ROOT/docs/swagger.json"
OUT="$HERE/../src/lib/api-schema.ts"
TMP="$(mktemp --suffix=.json)"
trap 'rm -f "$TMP"' EXIT

if [ ! -f "$SWAGGER" ]; then
  echo "swagger.json not found at $SWAGGER — run 'make swagger' first" >&2
  exit 1
fi

cd "$HERE/.."
npx swagger2openapi "$SWAGGER" --outfile "$TMP" > /dev/null
npx openapi-typescript "$TMP" --output "$OUT"

echo "Generated $OUT"
