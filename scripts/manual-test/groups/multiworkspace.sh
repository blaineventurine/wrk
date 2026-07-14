#!/usr/bin/env bash
# Multi-workspace semantics — the properties that only exist with two or
# more live worktrees: variant coexistence, gc pin survival, the
# loser-skips-hook provisioning contract, un-fingerprinted shared
# mutation visibility, cross-workspace concurrency, and storage-side
# glob provisioning into a fresh worktree.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

section W "multi-workspace — variant coexistence and gc pin survival"

subsec "W.1: second workspace reuses the variant without re-running the hook"
D=$SCRATCH/W1/main
mkdir -p "$D"
HOOKLOG=$SCRATCH/W1/hook-runs.log
: > "$HOOKLOG"
mkrepo git "$D"
seed "$D"
cat > "$D/.wrk.yml" <<YAML
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "echo run >> $HOOKLOG && mkdir -p '{shared}' && echo v1 > '{shared}/content.txt'"
          cwd: "{root}"
YAML
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/W1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S new w1-feature" )
FEATURE=$SCRATCH/W1/w1-feature
runs=$(wc -l < "$HOOKLOG" | tr -d ' ')
if [ "$runs" = "1" ]; then
  _mark_pass; echo "  PASS: W.1 hook ran exactly once across two workspaces" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.1 hook ran $runs times, want 1" | tee -a "$TRANSCRIPT"
fi
if [ "$(readlink "$D/node_modules")" = "$(readlink "$FEATURE/node_modules")" ]; then
  _mark_pass; echo "  PASS: W.1 both workspaces pinned to the same variant" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.1 workspaces on different variants" | tee -a "$TRANSCRIPT"
fi
# Absolute-path needles break on /var→/private canonicalization; the
# basename is unique within the fixture and stable.
( cd "$D" && expect_contains "w1-feature" "$WRK --storage $S workspaces" )
( cd "$D" && expect_contains "w1-feature" "$WRK --storage $S status --all" )

subsec "W.2: divergent manifests → two variants; gc keeps BOTH while pinned"
# Diverge the feature's manifest on its own branch so the worktree is clean.
echo '{"v":"feature"}' > "$FEATURE/package.json"
( cd "$FEATURE" && git add package.json && git commit -q -m diverge )
rm -f "$FEATURE/node_modules"
( cd "$FEATURE" && expect_exit 0 "$WRK --storage $S link" )
runs=$(wc -l < "$HOOKLOG" | tr -d ' ')
if [ "$runs" = "2" ]; then
  _mark_pass; echo "  PASS: W.2 new fingerprint provisioned via hook (2 runs total)" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.2 hook runs=$runs, want 2" | tee -a "$TRANSCRIPT"
fi
MAIN_TARGET=$(readlink "$D/node_modules")
FEATURE_TARGET=$(readlink "$FEATURE/node_modules")
if [ "$MAIN_TARGET" != "$FEATURE_TARGET" ]; then
  _mark_pass; echo "  PASS: W.2 workspaces on distinct variants" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.2 both on $MAIN_TARGET" | tee -a "$TRANSCRIPT"
fi
# THE core safety property: gc must keep a variant pinned by ANY live
# workspace — with both pinned there is nothing to sweep.
( cd "$D" && expect_contains "Nothing to do" "$WRK --storage $S gc --dry-run" )
if [ -d "$MAIN_TARGET" ] && [ -d "$FEATURE_TARGET" ]; then
  _mark_pass; echo "  PASS: W.2 both variants on disk after gc --dry-run" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.2 a pinned variant vanished" | tee -a "$TRANSCRIPT"
fi

subsec "W.3: removing the feature unpins its variant; gc sweeps exactly that one"
( cd "$D" && expect_exit 0 "$WRK --storage $S remove w1-feature --yes" )
( cd "$D" && expect_contains "$(basename "$FEATURE_TARGET")" "$WRK --storage $S gc --dry-run" )
( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )
if [ ! -e "$FEATURE_TARGET" ] && [ -d "$MAIN_TARGET" ]; then
  _mark_pass; echo "  PASS: W.3 unpinned variant swept, pinned variant survived" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.3 swept wrong set (feature=$([ -e "$FEATURE_TARGET" ] && echo present || echo gone) main=$([ -d "$MAIN_TARGET" ] && echo present || echo gone))" | tee -a "$TRANSCRIPT"
fi
if [ -f "$D/node_modules/content.txt" ]; then
  _mark_pass; echo "  PASS: W.3 primary workspace still healthy after gc" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.3 primary symlink dangles after gc" | tee -a "$TRANSCRIPT"
fi

subsec "W.4: un-fingerprinted resource — mutation via one workspace visible in the other"
D=$SCRATCH/W4/main
mkdir -p "$D"
mkrepo git "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: data
    path: data-dir
YAML
printf 'demo\n' > "$D/README.md"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/data-dir"
echo one > "$D/data-dir/shared.txt"
S=$SCRATCH/storage/W4
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S new w4-feature" )
F=$SCRATCH/W4/w4-feature
echo two > "$F/data-dir/shared.txt"
if [ "$(cat "$D/data-dir/shared.txt")" = "two" ]; then
  _mark_pass; echo "  PASS: W.4 mutation through one workspace visible in the other" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: W.4 shared mutation not visible" | tee -a "$TRANSCRIPT"
fi

section X "multi-workspace — concurrency"

subsec "X.1: two workspaces race to provision; flock serializes, hook runs once"
D=$SCRATCH/X1/main
mkdir -p "$D"
HOOKLOG=$SCRATCH/X1/hook-runs.log
: > "$HOOKLOG"
mkrepo git "$D"
seed "$D"
cat > "$D/.wrk.yml" <<YAML
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "echo run >> $HOOKLOG && sleep 1 && mkdir -p '{shared}' && echo done > '{shared}/f.txt'"
          cwd: "{root}"
YAML
( cd "$D" && git add -A && git commit -q -m init )
W2=$SCRATCH/X1/second
( cd "$D" && git worktree add -q -b second "$W2" > /dev/null 2>&1 )
S=$SCRATCH/storage/X1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 ) &
PID1=$!
( cd "$W2" && "$WRK" --storage "$S" link > /dev/null 2>&1 ) &
PID2=$!
wait "$PID1"; EC1=$?
wait "$PID2"; EC2=$?
if [ "$EC1" = "0" ] && [ "$EC2" = "0" ]; then
  _mark_pass; echo "  PASS: X.1 both racing links exited 0" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: X.1 exits: $EC1 / $EC2" | tee -a "$TRANSCRIPT"
fi
runs=$(wc -l < "$HOOKLOG" | tr -d ' ')
if [ "$runs" = "1" ]; then
  _mark_pass; echo "  PASS: X.1 hook executed exactly once under the race" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: X.1 hook executed $runs times under the race" | tee -a "$TRANSCRIPT"
fi
T1=$(readlink "$D/node_modules"); T2=$(readlink "$W2/node_modules")
if [ -n "$T1" ] && [ "$T1" = "$T2" ] && [ -f "$D/node_modules/f.txt" ]; then
  _mark_pass; echo "  PASS: X.1 both workspaces linked to the winner's variant" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: X.1 targets diverge (t1=$T1 t2=$T2)" | tee -a "$TRANSCRIPT"
fi

section Y "multi-workspace — storage-side glob provisioning"

subsec "Y.1: fresh worktree gets glob resources provisioned from storage"
D=$SCRATCH/Y1/main
mkdir -p "$D"
mkrepo git "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: pkgs
    path: packages/*/node_modules
YAML
mkdir -p "$D/packages/a"
printf 'keep\n' > "$D/packages/a/keep"
printf 'demo\n' > "$D/README.md"
( cd "$D" && git add -A && git commit -q -m init )
# node_modules is untracked, exactly like real life — a fresh worktree
# will NOT have it on disk, so only the storage-side glob can find it.
mkdir -p "$D/packages/a/node_modules"
echo dep > "$D/packages/a/node_modules/dep.js"
S=$SCRATCH/storage/Y1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S new y1-feature" )
F=$SCRATCH/Y1/y1-feature
if [ -L "$F/packages/a/node_modules" ] && [ -f "$F/packages/a/node_modules/dep.js" ]; then
  _mark_pass; echo "  PASS: Y.1 glob resource linked into the fresh worktree from storage" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Y.1 glob resource missing in fresh worktree" | tee -a "$TRANSCRIPT"
fi
( cd "$F" && expect_exit 0 "$WRK --storage $S status --exit-code" )

section Z "multi-workspace — config drift and sibling clones"

subsec "Z.1: sibling worktree with a DRIFTED config keeps its pin across gc"
D=$SCRATCH/Z1/main
mkdir -p "$D"
mkrepo git "$D"
seed "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo main > '{shared}/who.txt'"
YAML
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/Z1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S new z1feat" )
FEAT=$SCRATCH/Z1/z1feat
# The feature moves the resource to web/node_modules on its own branch.
cat > "$FEAT/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: web/node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo feat > '{shared}/who.txt'"
YAML
printf '{"v":"feature"}\n' > "$FEAT/package.json"
( cd "$FEAT" && git add .wrk.yml package.json && git commit -q -m drift )
rm -f "$FEAT/node_modules"
( cd "$FEAT" && expect_exit 0 "$WRK --storage $S link" )
FT=$(readlink "$FEAT/web/node_modules")
# gc from MAIN, whose config knows nothing about web/node_modules:
# pre-fix this deleted the feature's variant (report finding H2).
( cd "$D" && expect_exit 0 "$WRK --storage $S gc --yes" )
if [ -d "$FT" ] && [ -f "$FEAT/web/node_modules/who.txt" ]; then
  _mark_pass; echo "  PASS: Z.1 drifted sibling's variant survived gc from main" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Z.1 drifted sibling's variant was swept (H2 regression)" | tee -a "$TRANSCRIPT"
fi

subsec "Z.2: a second CLONE sharing storage is protected by the clone registry"
URL="https://example.invalid/org/z2repo.git"
A=$SCRATCH/Z2/cloneA
mkdir -p "$A"
mkrepo git "$A"
( cd "$A" && git remote add origin "$URL" )
cat > "$A/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo v > '{shared}/c.txt'"
YAML
printf '{"v":"shared"}\n' > "$A/package.json"
printf 'demo\n' > "$A/README.md"
( cd "$A" && git add -A && git commit -q -m init )
B=$SCRATCH/Z2/cloneB
git clone -q "$A" "$B" 2>/dev/null
( cd "$B" && git remote set-url origin "$URL" )
S=$SCRATCH/storage/Z2
( cd "$A" && expect_exit 0 "$WRK --storage $S link" )
( cd "$B" && expect_exit 0 "$WRK --storage $S link" )
# Diverge clone B onto its own fingerprint.
printf '{"v":"cloneB"}\n' > "$B/package.json"
rm -f "$B/node_modules"
( cd "$B" && expect_exit 0 "$WRK --storage $S link" )
TB=$(readlink "$B/node_modules")
# gc from clone A: pre-fix it deleted B's pinned variant (finding H3).
( cd "$A" && expect_exit 0 "$WRK --storage $S gc --yes" )
if [ -d "$TB" ] && [ -f "$B/node_modules/c.txt" ]; then
  _mark_pass; echo "  PASS: Z.2 cloneB's pin survived gc from cloneA" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Z.2 cloneB's variant swept by cloneA's gc (H3 regression)" | tee -a "$TRANSCRIPT"
fi
# forget from A must refuse while B shares the storage.
( cd "$A" && expect_contains "other clone" "$WRK --storage $S forget --yes 2>&1" )
if [ -d "$TB" ]; then
  _mark_pass; echo "  PASS: Z.2 refused forget left storage intact" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Z.2 refused forget still deleted storage" | tee -a "$TRANSCRIPT"
fi
( cd "$A" && expect_exit 0 "$WRK --storage $S forget --yes --force" )
