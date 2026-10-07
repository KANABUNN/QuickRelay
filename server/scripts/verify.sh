#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
[ -z "$(gofmt -l cmd internal)" ] || { printf '%s\n' 'Go formatting required'; exit 1; }
go mod verify
go test ./...
go vet ./...
mkdir -p .local
CGO_ENABLED=0 go build -trimpath -o .local/quakerelay ./cmd/quakerelay
CGO_ENABLED=0 go build -trimpath -o .local/apns-send ./cmd/apns-send
python3 scripts/smoke.py --binary .local/quakerelay
# The race detector requires a C compiler even though production uses pure Go SQLite.
if command -v cc >/dev/null 2>&1; then
  CGO_ENABLED=1 go test -race ./...
else
  printf '%s\n' 'SKIP: race detector requires a C compiler'
fi
