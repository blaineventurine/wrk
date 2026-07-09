#!/usr/bin/env bash
# `wrk forget` — full-repo storage teardown.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! "$WRK" forget --help > /dev/null 2>&1; then
  echo "SKIP: wrk forget not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

forget_config() {
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

# storage_subtree prints the on-disk path of <storage>/<repo-id> for the
# given fixture (repo dir + storage root). Not deterministic across
# runs — but it's just a single dir named "local".
storage_subtree() {
  local storage="$1"
  # For local repos wrk uses `local/<hash>/` under the storage root.
  find "$storage" -mindepth 2 -maxdepth 2 -type d 2>/dev/null | head -n 1
}

section H "wrk forget — happy path"

subsec "H.1: forget --yes removes storage, .wrk.yml preserved"
D=$SCRATCH/H1
mkrepo git "$D"
seed "$D"
forget_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
SUBTREE=$(storage_subtree "$S")
if [ -z "$SUBTREE" ] || [ ! -d "$SUBTREE" ]; then
  echo "FAIL: H.1 setup — no storage subtree found under $S" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_exit 0 "$WRK --storage $S forget --yes" )
  if [ -d "$SUBTREE" ]; then
    echo "FAIL: H.1 storage subtree survived forget: $SUBTREE" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: H.1 storage subtree gone" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
  if [ ! -f "$D/.wrk.yml" ]; then
    echo "FAIL: H.1 .wrk.yml disappeared" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: H.1 .wrk.yml preserved" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "H.2: wrk link after forget re-provisions cleanly"
D=$SCRATCH/H2
mkrepo git "$D"
seed "$D"
forget_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H2
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
( cd "$D" && "$WRK" --storage "$S" forget --yes > /dev/null 2>&1 )
rm -f "$D/node_modules"
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
SUBTREE=$(storage_subtree "$S")
if [ -z "$SUBTREE" ]; then
  echo "FAIL: H.2 wrk link after forget did not create storage" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: H.2 re-provisioning worked" | tee -a "$TRANSCRIPT"
  _mark_pass
fi

subsec "H.3: --dry-run prints plan without touching disk"
D=$SCRATCH/H3
mkrepo git "$D"
seed "$D"
forget_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H3
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
SUBTREE_BEFORE=$(storage_subtree "$S")
( cd "$D" && expect_contains "Forgetting" "$WRK --storage $S forget --dry-run" )
SUBTREE_AFTER=$(storage_subtree "$S")
if [ "$SUBTREE_BEFORE" = "$SUBTREE_AFTER" ] && [ -n "$SUBTREE_AFTER" ]; then
  echo "PASS: H.3 --dry-run left storage untouched" | tee -a "$TRANSCRIPT"
  _mark_pass
else
  echo "FAIL: H.3 storage changed under --dry-run (before=$SUBTREE_BEFORE after=$SUBTREE_AFTER)" | tee -a "$TRANSCRIPT"
  _mark_fail
fi

section I "wrk forget — refusals"

subsec "I.1: populated registry → refuse without --force"
D=$SCRATCH/I1
mkrepo git "$D"
seed "$D"
forget_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/I1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )

# Seed a detach-registry entry.
COMMON_DIR=$( cd "$D" && git rev-parse --git-common-dir 2>/dev/null )
case "$COMMON_DIR" in
  /*) ;;
  *)  COMMON_DIR="$D/$COMMON_DIR" ;;
esac
REG_DIR="$COMMON_DIR/wrk"
mkdir -p "$REG_DIR"
CANON_D=$(cd "$D" && pwd -P)
printf '{%s:["node_modules"]}' "\"$CANON_D\"" > "$REG_DIR/detached.json"

( cd "$D" && expect_contains "detached" "$WRK --storage $S forget --yes 2>&1" )
# Storage should still be intact (refusal blocked execution).
SUBTREE=$(storage_subtree "$S")
if [ -n "$SUBTREE" ] && [ -d "$SUBTREE" ]; then
  echo "PASS: I.1 storage intact under refusal" | tee -a "$TRANSCRIPT"
  _mark_pass
else
  echo "FAIL: I.1 storage disappeared despite refusal" | tee -a "$TRANSCRIPT"
  _mark_fail
fi

subsec "I.2: --force overrides refusal and removes storage"
( cd "$D" && expect_exit 0 "$WRK --storage $S forget --force" )
SUBTREE=$(storage_subtree "$S")
if [ -z "$SUBTREE" ] || [ ! -d "$SUBTREE" ]; then
  echo "PASS: I.2 --force overrode refusal" | tee -a "$TRANSCRIPT"
  _mark_pass
else
  echo "FAIL: I.2 --force did not remove storage" | tee -a "$TRANSCRIPT"
  _mark_fail
fi

subsec "I.3: non-TTY without --yes → refuse"
D=$SCRATCH/I3
mkrepo git "$D"
seed "$D"
forget_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/I3
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
( cd "$D" && "$WRK" --storage "$S" forget < /dev/null > /dev/null 2>&1 )
ec=$?
if [ "$ec" = "0" ]; then
  echo "FAIL: I.3 non-TTY without --yes exited 0" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: I.3 non-TTY without --yes refused (exit=$ec)" | tee -a "$TRANSCRIPT"
  _mark_pass
fi
