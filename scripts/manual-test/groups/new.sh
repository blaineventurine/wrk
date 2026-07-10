#!/usr/bin/env bash
# `wrk new` — workspace creation, including --base.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

section K "wrk new — sibling worktree provisioning"

# minsetup <path> — turn <path> into a fresh git repo with a committed
# minimal .wrk.yml, so `wrk new` finds a config on the primary and has
# a HEAD to build a worktree from. Deliberately does NOT call the
# `seed` helper (which drops node_modules/vendor bait that turns
# wrk's link step into a side-effect fest — irrelevant to `--base`).
minsetup() {
  local path="$1"
  mkdir -p "$path"
  (
    cd "$path"
    git init -q -b main
    git config user.email t@t
    git config user.name t
    printf 'resources: []\n' > .wrk.yml
    printf 'wrk-manual-test\n' > README
    git add -A
    git commit -q -m init
  )
}

subsec "K.1: wrk new without --base uses current HEAD"
D=$SCRATCH/K1/main
minsetup "$D"
( cd "$D" && expect_exit 0 "$WRK new K1-child" )
CHILD="$SCRATCH/K1/K1-child"
if [ -d "$CHILD" ]; then
  echo "PASS: K.1 sibling worktree exists at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_pass
else
  echo "FAIL: K.1 no sibling worktree at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_fail
fi

subsec "K.2: wrk new --base <branch> forks a new branch off <base>"
D=$SCRATCH/K2/main
minsetup "$D"
( cd "$D" && git branch feature-base )
( cd "$D" && expect_exit 0 "$WRK new K2-child --base feature-base" )
CHILD="$SCRATCH/K2/K2-child"
if [ -d "$CHILD" ]; then
  BRANCH=$(cd "$CHILD" && git rev-parse --abbrev-ref HEAD)
  if [ "$BRANCH" = "K2-child" ]; then
    echo "PASS: K.2 new worktree on branch K2-child (forked off feature-base)" | tee -a "$TRANSCRIPT"
    _mark_pass
  else
    echo "FAIL: K.2 branch=$BRANCH, want K2-child" | tee -a "$TRANSCRIPT"
    _mark_fail
  fi
else
  echo "FAIL: K.2 no worktree at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_fail
fi

subsec "K.3: wrk new --base <nonexistent> errors cleanly, no worktree left behind"
D=$SCRATCH/K3/main
minsetup "$D"
( cd "$D" && expect_exit 2 "$WRK new K3-child --base nope-not-a-ref" )
CHILD="$SCRATCH/K3/K3-child"
if [ ! -e "$CHILD" ]; then
  echo "PASS: K.3 no orphan worktree at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_pass
else
  echo "FAIL: K.3 orphan worktree present at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_fail
fi

subsec "K.4: wrk new --base --dry-run announces the base in the preview"
D=$SCRATCH/K4/main
minsetup "$D"
( cd "$D" && git branch feature-base )
( cd "$D" && expect_contains "based on feature-base" "$WRK new K4-child --base feature-base --dry-run" )
CHILD="$SCRATCH/K4/K4-child"
if [ ! -e "$CHILD" ]; then
  echo "PASS: K.4 dry-run left no worktree at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_pass
else
  echo "FAIL: K.4 dry-run created worktree at $CHILD" | tee -a "$TRANSCRIPT"
  _mark_fail
fi
