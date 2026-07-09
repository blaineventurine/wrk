#!/usr/bin/env bash
# `wrk remove` — feature workspace removal.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! "$WRK" remove --help > /dev/null 2>&1; then
  echo "SKIP: wrk remove not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

# remove_config writes a minimal .wrk.yml with no resources, sufficient for
# wrk new to succeed without fingerprinting or provisioning.
remove_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources: []
YAML
}

section E "wrk remove — happy path"

subsec "E.1: wrk remove <feature> --yes tears down the feature (bare name)"
D=$SCRATCH/E1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new e1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/e1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: E.1 wrk new did not create $FEATURE" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_exit 0 "$WRK remove e1feature --yes" )
  if [ -e "$FEATURE" ]; then
    echo "FAIL: E.1 feature dir survived remove" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: E.1 feature dir gone after remove" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "E.2: wrk remove with absolute path argument"
D=$SCRATCH/E2
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new e2feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/e2feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: E.2 wrk new did not create $FEATURE" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_exit 0 "$WRK remove $FEATURE --yes" )
  if [ -e "$FEATURE" ]; then
    echo "FAIL: E.2 feature dir survived remove (absolute-path)" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: E.2 absolute-path remove worked" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "E.3: --dry-run prints plan without touching disk"
D=$SCRATCH/E3
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new e3feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/e3feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: E.3 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_contains "Removing workspace" "$WRK remove e3feature --dry-run" )
  if [ ! -d "$FEATURE" ]; then
    echo "FAIL: E.3 --dry-run deleted the feature dir" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: E.3 --dry-run left disk untouched" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

section F "wrk remove — refusals"

subsec "F.1: target = primary (invoked from feature) → refuse with 'primary'"
D=$SCRATCH/F1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new f1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/f1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: F.1 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Invoke from the feature workspace, remove primary by absolute path.
  ( cd "$FEATURE" && expect_contains "primary" "$WRK remove $D --yes 2>&1" )
fi

subsec "F.2: target = current workspace → refuse with 'current'"
D=$SCRATCH/F2
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new f2feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/f2feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: F.2 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Invoke from the feature workspace targeting itself.
  ( cd "$FEATURE" && expect_contains "current" "$WRK remove $FEATURE --yes 2>&1" )
fi

subsec "F.3: non-TTY without --yes → refuse (exit non-zero)"
D=$SCRATCH/F3
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new f3feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/f3feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: F.3 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && "$WRK" remove f3feature < /dev/null > /dev/null 2>&1 )
  ec=$?
  if [ "$ec" = "0" ]; then
    echo "FAIL: F.3 non-TTY without --yes exited 0" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: F.3 non-TTY without --yes refused (exit=$ec)" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

section G "wrk remove — ghost redirect"

subsec "G.1: ghost with stranded registry entry → refuse with 'wrk gc' hint"
D=$SCRATCH/G1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new g1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/g1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: G.1 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Seed a detach-registry entry so the ghost hint fires. The registry
  # lives at <git-common-dir>/wrk/detached.json (git backend). We use
  # `git rev-parse --git-common-dir` for portability.
  COMMON_DIR=$( cd "$D" && git rev-parse --git-common-dir 2>/dev/null )
  # git rev-parse may return a relative path; resolve it against the primary.
  case "$COMMON_DIR" in
    /*) ;;
    *)  COMMON_DIR="$D/$COMMON_DIR" ;;
  esac
  REG_DIR="$COMMON_DIR/wrk"
  mkdir -p "$REG_DIR"
  # Canonicalize the feature path to match what wrk canonicalizes at plan time.
  CANON_FEATURE=$(cd "$FEATURE" && pwd -P)
  printf '{%s:["node_modules"]}' "\"$CANON_FEATURE\"" > "$REG_DIR/detached.json"

  # Hard-remove the worktree dir so `git worktree list` marks it prunable.
  rm -rf "$FEATURE"

  # wrk remove <ghost> should refuse with a wrk gc hint.
  ( cd "$D" && expect_contains "wrk gc" "$WRK remove g1feature 2>&1" )
fi
