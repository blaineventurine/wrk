#!/usr/bin/env bash
# `wrk run` — force re-run of a resource's initialize hook against the
# existing shared variant. Contract:
#   - Successful invocations re-execute the hook (observable via a
#     timestamp file the hook writes).
#   - Unknown resource names error out.
#   - --dry-run prints the plan without touching disk.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! "$WRK" run --help > /dev/null 2>&1; then
  echo "SKIP: wrk run not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

# run_config writes a single-resource .wrk.yml whose initialize hook
# stamps a nanosecond timestamp into the shared variant. Two successive
# `wrk run` invocations that both succeed MUST leave different bytes on
# disk — that's the observable "hook actually re-ran" signal.
run_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && date +%s%N > '{shared}/timestamp.txt'"
          cwd: "{root}"
YAML
}

section H "wrk run — force re-run of initialize hook"

subsec "H.1: wrk run <resource> re-executes the hook"
D=$SCRATCH/H1
mkrepo git "$D"
seed "$D"
run_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
timestamp1=$(cat "$D/node_modules/timestamp.txt" 2>/dev/null || echo "")
# date +%s%N resolution is nanoseconds, but sh built-in may fall back
# to seconds on macOS. Sleep beyond the largest observed granularity.
sleep 1
( cd "$D" && expect_exit 0 "$WRK --storage $S run node --yes" )
timestamp2=$(cat "$D/node_modules/timestamp.txt" 2>/dev/null || echo "")
if [ -n "$timestamp1" ] && [ -n "$timestamp2" ] && [ "$timestamp1" != "$timestamp2" ]; then
  _mark_pass
  printf '  PASS: timestamp changed (%s -> %s)\n' "$timestamp1" "$timestamp2" | tee -a "$TRANSCRIPT"
else
  _mark_fail
  printf '  FAIL: timestamps did not change (before=%s after=%s)\n' "$timestamp1" "$timestamp2" | tee -a "$TRANSCRIPT"
fi

subsec "H.2: wrk run on unknown resource errors"
D=$SCRATCH/H2
mkrepo git "$D"
seed "$D"
run_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H2
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_contains "not configured" "$WRK --storage $S run nonexistent" )

subsec "H.3: wrk run --dry-run prints a plan without touching disk"
D=$SCRATCH/H3
mkrepo git "$D"
seed "$D"
run_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H3
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
timestamp1=$(cat "$D/node_modules/timestamp.txt" 2>/dev/null || echo "")
sleep 1
( cd "$D" && expect_exit 0 "$WRK --storage $S run node --dry-run" )
timestamp2=$(cat "$D/node_modules/timestamp.txt" 2>/dev/null || echo "")
if [ -n "$timestamp1" ] && [ "$timestamp1" = "$timestamp2" ]; then
  _mark_pass
  printf '  PASS: timestamp unchanged under --dry-run\n' | tee -a "$TRANSCRIPT"
else
  _mark_fail
  printf '  FAIL: --dry-run changed the timestamp (before=%s after=%s)\n' "$timestamp1" "$timestamp2" | tee -a "$TRANSCRIPT"
fi
