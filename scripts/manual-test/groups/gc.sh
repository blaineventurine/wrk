#!/usr/bin/env bash
# `wrk gc` — variant pruning, ghost-workspace sweep, bookkeeping cleanup.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! "$WRK" gc --help > /dev/null 2>&1; then
  echo "SKIP: wrk gc not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

# gc_config writes a minimal single-resource .wrk.yml so tests can produce
# exactly one variant dir per package.json content.
gc_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && touch '{shared}/.installed'"
YAML
}

# count_variants prints the number of variant subdirectories under
# <storage>/*/node_modules/. Variant dirs are named by hex hashes; the
# bookkeeping siblings (`.wrk-lock`, `.wrk-provisioning`, `.wrk-deleting`)
# have dotted suffixes and are excluded.
count_variants() {
  local storage="$1"
  find "$storage" -mindepth 4 -maxdepth 4 -type d 2>/dev/null \
    | grep -Ev '\.(wrk-lock|wrk-tmp|wrk-backup|wrk-deleting|wrk-forgetting|wrk-provisioning)$' \
    | wc -l | tr -d ' '
}

# find_one_variant_dir prints the absolute path of the first (any) variant
# dir under storage. Used by C.1 / C.2 to seed bookkeeping in the right place.
find_one_variant_dir() {
  local storage="$1"
  find "$storage" -mindepth 4 -maxdepth 4 -type d 2>/dev/null \
    | grep -Ev '\.(wrk-lock|wrk-tmp|wrk-backup|wrk-deleting|wrk-forgetting|wrk-provisioning)$' \
    | head -n 1
}

section A "wrk gc — variant pruning"

subsec "A.1: single workspace, one variant → nothing to do"
D=$SCRATCH/A1
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/A1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_contains "Nothing to do" "$WRK --storage $S gc --dry-run" )

subsec "A.2: two variants (one stale) → gc --yes removes stale"
D=$SCRATCH/A2
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/A2
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
# Mutate the fingerprint input; the previous symlink stays in place so
# Link builds a new variant beside the old.
echo '{"v":2}' > "$D/package.json"
rm -f "$D/node_modules"
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

before=$(count_variants "$S")
if [ "$before" != "2" ]; then
  echo "FAIL: A.2 expected 2 variants before gc, found $before" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: A.2 setup produced 2 variants" | tee -a "$TRANSCRIPT"
  _mark_pass
  ( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )
  after=$(count_variants "$S")
  if [ "$after" != "1" ]; then
    echo "FAIL: A.2 expected 1 variant after gc, found $after" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: A.2 stale variant pruned (2 → 1)" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "A.3: --dry-run does not touch disk"
D=$SCRATCH/A3
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/A3
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
echo '{"v":2}' > "$D/package.json"
rm -f "$D/node_modules"
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

( cd "$D" && expect_contains "Total:" "$WRK --storage $S gc --dry-run" )
after=$(count_variants "$S")
if [ "$after" != "2" ]; then
  echo "FAIL: A.3 --dry-run mutated storage (variants now $after, want 2)" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: A.3 --dry-run left both variants intact" | tee -a "$TRANSCRIPT"
  _mark_pass
fi

section B "wrk gc — ghost workspaces"

subsec "B.1: rm -rf feature/ without git worktree prune → gc prunes it"
D=$SCRATCH/B1
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/B1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

# Create a feature worktree, then hard-delete it.
FEATURE=$SCRATCH/B1-feature
( cd "$D" && git worktree add -b feature "$FEATURE" 2>&1 | tail -1 | tee -a "$TRANSCRIPT" )
rm -rf "$FEATURE"

# git worktree list should still reference the ghost before gc.
if ! ( cd "$D" && git worktree list --porcelain | grep -qE '^worktree.*B1-feature' ); then
  echo "FAIL: B.1 setup — ghost worktree not in git worktree list" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: B.1 ghost worktree present before gc" | tee -a "$TRANSCRIPT"
  _mark_pass

  ( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )

  # After gc, the ghost should no longer be listed.
  if ( cd "$D" && git worktree list --porcelain | grep -qE '^worktree.*B1-feature' ); then
    echo "FAIL: B.1 ghost survived gc" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: B.1 ghost pruned by gc" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

section C "wrk gc — bookkeeping cleanup"

subsec "C.1: orphaned .wrk-lock (no matching variant) → gc removes it"
D=$SCRATCH/C1
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/C1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

VDIR=$(find_one_variant_dir "$S")
if [ -z "$VDIR" ]; then
  echo "FAIL: C.1 setup — could not find a variant dir under $S" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Seed an orphaned lock alongside real variants: same parent dir, name has
  # no matching variant subdir.
  PARENT=$(dirname "$VDIR")
  ORPHAN_LOCK="$PARENT/deadbeefdeadbeef.wrk-lock"
  : > "$ORPHAN_LOCK"

  ( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )

  if [ -e "$ORPHAN_LOCK" ]; then
    echo "FAIL: C.1 orphaned lock survived gc: $ORPHAN_LOCK" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: C.1 orphaned lock removed" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "C.2: stale .wrk-deleting marker → gc sweeps it"
D=$SCRATCH/C2
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/C2
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

VDIR=$(find_one_variant_dir "$S")
if [ -z "$VDIR" ]; then
  echo "FAIL: C.2 setup — could not find a variant dir under $S" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  PARENT=$(dirname "$VDIR")
  STALE_MARKER="$PARENT/cafebabecafebabe.wrk-deleting"
  mkdir -p "$STALE_MARKER"
  echo "leftover" > "$STALE_MARKER/leftover.txt"

  ( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )

  if [ -e "$STALE_MARKER" ]; then
    echo "FAIL: C.2 stale .wrk-deleting survived gc: $STALE_MARKER" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: C.2 stale .wrk-deleting swept" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

section D "wrk gc — safety gates"

subsec "D.1: piped stdin without --yes → refuses"
D=$SCRATCH/D1
mkrepo git "$D"
seed "$D"
gc_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/D1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
echo '{"v":2}' > "$D/package.json"
rm -f "$D/node_modules"
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

# Non-TTY (stdin from /dev/null via eval in expect_exit) with no --yes.
# expect_exit checks exit code; we want any non-zero.
( cd "$D" && "$WRK" --storage "$S" gc < /dev/null > /dev/null 2>&1 )
ec=$?
if [ "$ec" = "0" ]; then
  echo "FAIL: D.1 non-TTY without --yes exited 0" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: D.1 non-TTY without --yes refused (exit=$ec)" | tee -a "$TRANSCRIPT"
  _mark_pass
fi
