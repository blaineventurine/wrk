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

subsec "H.4: wrk link recovers after fixing a failing hook"
D=$SCRATCH/H4
mkrepo git "$D"
seed "$D"
# The hook cat's a file the user hasn't created yet, so the initial
# link MUST fail. wrk's error path exits 2 (the "any other error" bucket
# in cmd/wrk/root.go); the C4 crash-safety contract guarantees the
# variant is NOT left half-materialized, so the next link re-runs the
# hook from scratch rather than skipping it.
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p {shared} && cat {root}/config-file > {shared}/output.txt"
YAML
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H4
# First link: hook fails because config-file doesn't exist.
( cd "$D" && expect_exit 2 "$WRK --storage $S link" )
# User fixes the missing input.
echo "fixed content" > "$D/config-file"
# Retry: hook now runs to completion and materializes the variant.
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if grep -q "fixed content" "$D/node_modules/output.txt" 2>/dev/null; then
  _mark_pass
  printf '  PASS: variant reflects the fixed hook output\n' | tee -a "$TRANSCRIPT"
else
  _mark_fail
  printf '  FAIL: variant does not reflect fixed hook output\n' | tee -a "$TRANSCRIPT"
fi

subsec "H.5: wrk run --yes refreshes an existing variant with new hook output"
D=$SCRATCH/H5
mkrepo git "$D"
seed "$D"
# Same shape as H.4 but the input file exists from the start, so the
# first link succeeds. Then we mutate the input WITHOUT changing the
# fingerprint (fingerprint tracks package.json, not `data`) and force
# a re-run via `wrk run node --yes` — the --yes accommodates the
# destructive-confirmation gate on `wrk run` that ships in this release.
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p {shared} && cat {root}/data > {shared}/output.txt"
YAML
echo "v1" > "$D/data"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H5
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if grep -q "^v1$" "$D/node_modules/output.txt" 2>/dev/null; then
  _mark_pass
  printf '  PASS: initial link populated variant with v1\n' | tee -a "$TRANSCRIPT"
else
  _mark_fail
  printf '  FAIL: initial link did not populate variant with v1 (got: %s)\n' \
    "$(cat "$D/node_modules/output.txt" 2>/dev/null || echo '<missing>')" | tee -a "$TRANSCRIPT"
fi
# Mutate the input and force a re-run.
echo "v2" > "$D/data"
( cd "$D" && expect_exit 0 "$WRK --storage $S run node --yes" )
if grep -q "^v2$" "$D/node_modules/output.txt" 2>/dev/null; then
  _mark_pass
  printf '  PASS: wrk run --yes refreshed the variant to v2\n' | tee -a "$TRANSCRIPT"
else
  _mark_fail
  printf '  FAIL: wrk run --yes did not refresh the variant (got: %s)\n' \
    "$(cat "$D/node_modules/output.txt" 2>/dev/null || echo '<missing>')" | tee -a "$TRANSCRIPT"
fi

subsec "H.6: wrk run refuses a detached resource"
D=$SCRATCH/H6
mkrepo git "$D"
seed "$D"
run_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/H6
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_exit 2 "$WRK --storage $S run node --yes" )
( cd "$D" && expect_contains "detached" "$WRK --storage $S run node --yes 2>&1" )

subsec "H.7: wrk run refuses an isolated resource"
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --isolate --yes" )
( cd "$D" && expect_exit 2 "$WRK --storage $S run node --yes" )
( cd "$D" && expect_contains "isolated" "$WRK --storage $S run node --yes 2>&1" )
