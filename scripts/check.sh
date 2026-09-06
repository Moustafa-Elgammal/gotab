#!/usr/bin/env bash
# The gate. Green here is a precondition for marking any task done.
# Run it once at the end of a task, not per-assertion — see docs/PARALLEL-WORK.md.
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
# Checked against real imports via `go list`, not by grepping text -- a grep matches the rule's own
# prose in doc.go and reports a violation that isn't there. `-deps` also catches transitive ones.
step "core stays pure" bash -c '
  bad=$(go list -deps -f "{{.ImportPath}}" ./internal/core/... 2>/dev/null | grep -Ex "C|.*/internal/platform(/.*)?" || true)
  if [ -n "$bad" ]; then
    echo "internal/core must not depend on cgo or internal/platform, but does:"
    echo "$bad" | sed "s/^/    /"
    echo "  see docs/ARCHITECTURE.md"
    false
  fi'

exit $fail
