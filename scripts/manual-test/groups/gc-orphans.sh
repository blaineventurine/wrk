#!/usr/bin/env bash
# `wrk gc` — orphaned-storage sweep: subtrees no live workspace's config
# claims (resources removed/renamed in .wrk.yml) are offered for
# deletion; isolation pins and unreadable configs make the sweep
# conservative.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

two_config() {
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
  - name: tool
    path: old-tool
YAML
}

one_config() {
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

section O "wrk gc — orphaned storage"

subsec "O.1: resource removed from config → its storage is offered and swept"
D=$SCRATCH/O1
mkrepo git "$D"
seed "$D"
two_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/old-tool"
echo bin > "$D/old-tool/bin.txt"
S=$SCRATCH/storage/O1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
TOOL_STORE=$(readlink "$D/old-tool")
if [ -d "$TOOL_STORE" ]; then
  _mark_pass; echo "  PASS: O.1 setup — tool adopted at $TOOL_STORE" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: O.1 setup — tool not adopted" | tee -a "$TRANSCRIPT"
fi
# Drop the resource from the config and its symlink from the workspace.
one_config "$D"
rm -f "$D/old-tool"
( cd "$D" && expect_contains "Orphaned storage" "$WRK --storage $S gc --dry-run" )
( cd "$D" && expect_contains "old-tool" "$WRK --storage $S gc --dry-run" )
# --dry-run left it alone.
if [ -d "$TOOL_STORE" ]; then
  _mark_pass; echo "  PASS: O.1 dry-run kept the orphan" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: O.1 dry-run deleted the orphan" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )
if [ ! -e "$TOOL_STORE" ]; then
  _mark_pass; echo "  PASS: O.1 orphaned storage swept" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: O.1 orphan survived gc --yes" | tee -a "$TRANSCRIPT"
fi
# The still-configured resource must be untouched and healthy.
if [ -f "$D/node_modules/.installed" ]; then
  _mark_pass; echo "  PASS: O.1 configured resource survived the sweep" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: O.1 configured resource damaged" | tee -a "$TRANSCRIPT"
fi
# Second gc: nothing left to do.
( cd "$D" && expect_contains "Nothing to do" "$WRK --storage $S gc --dry-run" )

subsec "O.2: isolated variant of an unconfigured resource survives (registry pin)"
D=$SCRATCH/O2
mkrepo git "$D"
seed "$D"
two_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/old-tool"
echo bin > "$D/old-tool/bin.txt"
S=$SCRATCH/storage/O2
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --isolate --yes tool" )
ISO_TARGET=$(readlink "$D/old-tool")
case "$ISO_TARGET" in
  *isolated-*) _mark_pass; echo "  PASS: O.2 setup — tool isolated at $ISO_TARGET" | tee -a "$TRANSCRIPT" ;;
  *)           _mark_fail; echo "  FAIL: O.2 setup — tool not isolated: $ISO_TARGET" | tee -a "$TRANSCRIPT" ;;
esac
# Now drop `tool` from the config entirely. Its isolated variant is
# unreachable through any config — but the isolation registry pins it.
one_config "$D"
( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )
if [ -d "$ISO_TARGET" ] && [ -f "$D/old-tool/bin.txt" ]; then
  _mark_pass; echo "  PASS: O.2 isolated variant survived gc despite unconfigured resource" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: O.2 isolated variant lost (target=$ISO_TARGET)" | tee -a "$TRANSCRIPT"
fi

subsec "O.3: unreadable config in a sibling workspace → sweep skipped with a note"
D=$SCRATCH/O3/main
mkdir -p "$D"
mkrepo git "$D"
seed "$D"
two_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/old-tool"
echo bin > "$D/old-tool/bin.txt"
S=$SCRATCH/storage/O3
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
TOOL_STORE=$(readlink "$D/old-tool")
W2=$SCRATCH/O3/second
( cd "$D" && git worktree add -q -b second "$W2" > /dev/null 2>&1 )
# Sabotage the sibling's config and orphan the tool in the primary.
printf 'resources: [broken\n' > "$W2/.wrk.yml"
one_config "$D"
rm -f "$D/old-tool"
( cd "$D" && expect_contains "orphaned-storage sweep skipped" "$WRK --storage $S gc --dry-run 2>&1" )
( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )
if [ -d "$TOOL_STORE" ]; then
  _mark_pass; echo "  PASS: O.3 conservative skip kept the would-be orphan" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: O.3 gc deleted storage despite unreadable sibling config" | tee -a "$TRANSCRIPT"
fi
