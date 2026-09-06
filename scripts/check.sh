#!/usr/bin/env bash
# The gate. Green here is a precondition for marking any task done.
# Run it once at the end of a task, not per-assertion — see docs/AGENTS.md.
set -uo pipefail

cd "$(dirname "$0")/.."

red=$'\033[31m'; green=$'\033[32m'; off=$'\033[0m'
fail=0
step() {
  printf '==> %s\n' "$1"; shift
  if "$@"; then printf '%s    ok%s\n' "$green" "$off"
  else printf '%s    FAILED%s\n' "$red" "$off"; fail=1; fi
}

step "gofmt"   bash -c '[ -z "$(gofmt -l . 2>/dev/null)" ] || { gofmt -l .; false; }'
step "go vet"  go vet ./...
step "tests"   go test ./...

# The architectural invariant that matters most: core must never reach for the platform.
step "core stays pure" bash -c '
  if grep -rn "platform\|\"C\"" internal/core/ --include="*.go" 2>/dev/null | grep -v "_test.go"; then
    echo "internal/core must not import cgo or internal/platform (see docs/ARCHITECTURE.md)"; false
  fi'

exit $fail
