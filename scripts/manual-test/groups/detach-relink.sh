#!/usr/bin/env bash
# `wrk detach` / `wrk relink` — independence and reconnection, including
# the isolation exit (`relink` on an isolated resource reconnects it to
# the fingerprint variant and deletes the private copy).

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

dr_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo shared-v1 > '{shared}/content.txt'"
          cwd: "{root}"
YAML
}

section R "wrk detach / wrk relink — independence and reconnection"

subsec "R.1: detach materializes an independent copy; edits stay local"
D=$SCRATCH/R1
mkrepo git "$D"
seed "$D"
dr_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/R1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
SHARED_TARGET=$(readlink "$D/node_modules")
( cd "$D" && expect_contains "independent copy" "$WRK --storage $S detach --dry-run" )
# dry-run must not have touched the symlink.
if [ -L "$D/node_modules" ]; then
  _mark_pass; echo "  PASS: R.1 detach --dry-run left the symlink" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.1 detach --dry-run mutated the workspace" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
if [ ! -L "$D/node_modules" ] && [ -d "$D/node_modules" ] \
   && [ "$(cat "$D/node_modules/content.txt")" = "shared-v1" ]; then
  _mark_pass; echo "  PASS: R.1 detached copy is real and byte-identical" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.1 detached copy wrong" | tee -a "$TRANSCRIPT"
fi
# Local edit must NOT reach shared storage.
echo "local-edit" > "$D/node_modules/content.txt"
if [ "$(cat "$SHARED_TARGET/content.txt")" = "shared-v1" ]; then
  _mark_pass; echo "  PASS: R.1 local edit stayed local" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.1 local edit leaked into shared storage" | tee -a "$TRANSCRIPT"
fi
# detached is a resting state.
( cd "$D" && expect_contains "detached" "$WRK --storage $S status" )
( cd "$D" && expect_exit 0 "$WRK --storage $S status --exit-code" )

subsec "R.2: detach without --yes on a non-TTY refuses"
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 ) # noop; keep state
D2=$SCRATCH/R2
mkrepo git "$D2"
seed "$D2"
dr_config "$D2"
( cd "$D2" && git add -A && git commit -q -m init )
S2=$SCRATCH/storage/R2
( cd "$D2" && expect_exit 0 "$WRK --storage $S2 link" )
( cd "$D2" && expect_contains "requires --yes" "$WRK --storage $S2 detach < /dev/null" )
if [ -L "$D2/node_modules" ]; then
  _mark_pass; echo "  PASS: R.2 refusal left the symlink in place" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.2 refusal still detached" | tee -a "$TRANSCRIPT"
fi

subsec "R.3: relink discards the local edit and reconnects"
# Continues R.1's fixture: node_modules is detached with a local edit.
( cd "$D" && expect_contains "discard" "$WRK --storage $S relink --dry-run" )
if [ ! -L "$D/node_modules" ]; then
  _mark_pass; echo "  PASS: R.3 relink --dry-run left the detached copy" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.3 relink --dry-run mutated the workspace" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_contains "requires --yes" "$WRK --storage $S relink < /dev/null" )
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --yes" )
if [ -L "$D/node_modules" ] && [ "$(cat "$D/node_modules/content.txt")" = "shared-v1" ]; then
  _mark_pass; echo "  PASS: R.3 reconnected; local edit discarded" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.3 relink did not restore the shared view" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_contains "linked" "$WRK --storage $S status" )

subsec "R.4: plain relink exits isolation — variant deleted, registry cleared"
D=$SCRATCH/R4
mkrepo git "$D"
seed "$D"
dr_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/R4
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --isolate --yes" )
ISO_TARGET=$(readlink "$D/node_modules")
case "$ISO_TARGET" in
  *isolated-*) _mark_pass; echo "  PASS: R.4 setup — isolated at $ISO_TARGET" | tee -a "$TRANSCRIPT" ;;
  *)           _mark_fail; echo "  FAIL: R.4 setup — not isolated: $ISO_TARGET" | tee -a "$TRANSCRIPT" ;;
esac
# The plan announces the exit before consent.
( cd "$D" && expect_contains "Isolation exits:" "$WRK --storage $S relink --dry-run" )
# Dry-run must not have deleted the private variant.
if [ -d "$ISO_TARGET" ]; then
  _mark_pass; echo "  PASS: R.4 dry-run kept the isolated variant" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.4 dry-run deleted the isolated variant" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --yes" )
NEW_TARGET=$(readlink "$D/node_modules")
case "$NEW_TARGET" in
  *isolated-*) _mark_fail; echo "  FAIL: R.4 still isolated after relink" | tee -a "$TRANSCRIPT" ;;
  *)           _mark_pass; echo "  PASS: R.4 reconnected to fingerprint variant" | tee -a "$TRANSCRIPT" ;;
esac
if [ ! -e "$ISO_TARGET" ]; then
  _mark_pass; echo "  PASS: R.4 isolated variant deleted" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.4 isolated variant survived: $ISO_TARGET" | tee -a "$TRANSCRIPT"
fi
# Registry entry cleared: isolated.json (if present) no longer mentions the workspace.
REG="$D/.git/wrk/isolated.json"
if [ ! -f "$REG" ] || ! grep -qF "node_modules" "$REG"; then
  _mark_pass; echo "  PASS: R.4 isolation registry cleared" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.4 isolation registry still populated:" | tee -a "$TRANSCRIPT"
  sed 's/^/    /' "$REG" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_contains "linked" "$WRK --storage $S status" )

subsec "R.5: detach and link leave an isolated resource untouched"
D=$SCRATCH/R5
mkrepo git "$D"
seed "$D"
dr_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/R5
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --isolate --yes" )
ISO_TARGET=$(readlink "$D/node_modules")
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )   # must be a no-op
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )            # must be a no-op
if [ "$(readlink "$D/node_modules")" = "$ISO_TARGET" ] && [ -d "$ISO_TARGET" ]; then
  _mark_pass; echo "  PASS: R.5 isolation survived detach + link" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: R.5 isolation disturbed (now $(readlink "$D/node_modules" 2>/dev/null))" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_contains "isolated" "$WRK --storage $S status" )
( cd "$D" && expect_contains "isolated" "$WRK --storage $S workspaces" )
( cd "$D" && expect_exit 0 "$WRK --storage $S status --exit-code" )
