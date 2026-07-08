#!/usr/bin/env bash
# `wrk gc` — variant pruning, ghost-workspace sweep, bookkeeping cleanup.
#
# Skeleton: fills out incrementally as `wrk gc` is implemented.
# Every scenario below maps to the spec's Groups A/B/C/D.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

# The gc command doesn't exist yet — bail out cleanly.
if ! "$WRK" gc --help > /dev/null 2>&1; then
  echo "SKIP: wrk gc not implemented yet in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

section A "wrk gc — variant pruning"

subsec "A.1: single workspace, one variant → nothing to prune"
# TODO: build a single-workspace fixture, wrk link, then wrk gc, expect no
# variants removed. See spec Group A for the full plan.
echo "TODO: A.1" | tee -a "$TRANSCRIPT"

subsec "A.2: single workspace, three variants (two stale) → GC removes two"
echo "TODO: A.2" | tee -a "$TRANSCRIPT"

subsec "A.3: two workspaces pin different variants → GC removes the unused one"
echo "TODO: A.3" | tee -a "$TRANSCRIPT"

section B "wrk gc — ghost workspaces"

subsec "B.1: rm -rf feature/ without git worktree prune"
echo "TODO: B.1" | tee -a "$TRANSCRIPT"

subsec "B.2: git worktree prune already run"
echo "TODO: B.2" | tee -a "$TRANSCRIPT"

section C "wrk gc — bookkeeping cleanup"

subsec "C.1: orphaned .wrk-lock (variant already gone)"
echo "TODO: C.1" | tee -a "$TRANSCRIPT"

section D "wrk gc — concurrency"

subsec "D.1: concurrent wrk link holds .wrk-lock → GC skips"
echo "TODO: D.1" | tee -a "$TRANSCRIPT"

