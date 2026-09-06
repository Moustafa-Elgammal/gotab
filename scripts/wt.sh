#!/usr/bin/env bash
# Worktree helper for parallel agent work. One worktree per task, never shared.
#   scripts/wt.sh new P1.3     create ../gotab-wt/P1.3 on branch feat/P1.3
#   scripts/wt.sh list         show active worktrees
#   scripts/wt.sh done P1.3    merge into master, remove the worktree
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
WT_DIR="${ROOT}/../gotab-wt"

usage() { sed -n '2,6p' "$0" | sed 's/^# \?//'; exit 1; }

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
    git checkout master
    git merge --no-ff "feat/${id}" -m "merge: ${id}"
    git worktree remove "${WT_DIR}/${id}"
    git branch -d "feat/${id}"
    echo "merged and cleaned up ${id}"
    ;;
  *) usage ;;
esac
