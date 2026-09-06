#!/usr/bin/env bash
# Worktree helper for parallel agent work. One worktree per task, never shared.
#   scripts/wt.sh new P1.3     create .worktrees/P1.3 on branch feat/P1.3
#   scripts/wt.sh list         show active worktrees
#   scripts/wt.sh done P1.3    merge into main, remove the worktree
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
# Worktrees live inside the project, not in a sibling directory: the repo is then self-contained and
# a checkout cannot scatter directories over its parent. The leading dot is load-bearing — `go list`,
# `go vet` and `go build ./...` all skip directories beginning with "." or "_", so an in-progress
# worktree is invisible to the gate. `gofmt` does NOT skip them, which is why check.sh feeds gofmt the
# package list from `go list` rather than walking the tree.
WT_DIR="${ROOT}/.worktrees"

usage() { sed -n '2,5p' "$0" | sed -E 's/^# ?//'; exit 1; }

case "${1:-}" in
  new)
    id="${2:?task id required, e.g. P1.3}"
    git worktree add -b "feat/${id}" "${WT_DIR}/${id}" 2>/dev/null \
      || git worktree add "${WT_DIR}/${id}" "feat/${id}"
    echo "worktree ready: ${WT_DIR}/${id}  (branch feat/${id})"
    echo "give the agent: docs/ARCHITECTURE.md + docs/tasks/${id}.md — nothing else"
    ;;
  list)
    git worktree list
    ;;
  done)
    id="${2:?task id required}"
    git -C "${WT_DIR}/${id}" diff --quiet || { echo "uncommitted changes in ${id}; commit first"; exit 1; }
    git checkout main
    git merge --no-ff "feat/${id}" -m "merge: ${id}"
    git worktree remove "${WT_DIR}/${id}"
    git branch -d "feat/${id}"
    echo "merged and cleaned up ${id}"
    ;;
  *) usage ;;
esac
