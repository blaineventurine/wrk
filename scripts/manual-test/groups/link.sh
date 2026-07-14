#!/usr/bin/env bash
# `wrk link` — the core provisioning command: adoption, conflicts, the
# resource state machine, config validation, and the managed ignore
# block. Everything here runs against a scratch --storage so the user's
# real storage is never touched.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

# node_config <root> — a single hook-provisioned fingerprinted resource.
node_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo v1 > '{shared}/marker.txt'"
          cwd: "{root}"
YAML
}

# full_config <root> — node (hook) + data (adoptable, no hook) +
# env (create:false, provided out-of-band).
full_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo v1 > '{shared}/marker.txt'"
          cwd: "{root}"

  - name: data
    path: data-dir

  - name: env
    path: .env
    create: false
YAML
}

section L "wrk link — core provisioning"

subsec "L.1: hook provisions, adoption moves, create:false skips"
D=$SCRATCH/L1
mkrepo git "$D"
seed "$D"
full_config "$D"
rm -f "$D/.env"   # nothing anywhere for the create:false resource
( cd "$D" && git add -A && git commit -q -m init )
# Untracked local copy for wrk to adopt — created AFTER the commit, the
# way real node_modules/.env content exists.
mkdir -p "$D/data-dir"
echo "adopt-me" > "$D/data-dir/payload.txt"
S=$SCRATCH/storage/L1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
# node: hook-provisioned symlink.
if [ -L "$D/node_modules" ] && [ -f "$D/node_modules/marker.txt" ]; then
  _mark_pass; echo "  PASS: L.1 hook-provisioned resource linked" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.1 node_modules not a working symlink" | tee -a "$TRANSCRIPT"
fi
# data: adopted — workspace path is a symlink AND the content survived the move.
if [ -L "$D/data-dir" ] && [ "$(cat "$D/data-dir/payload.txt" 2>/dev/null)" = "adopt-me" ]; then
  _mark_pass; echo "  PASS: L.1 local copy adopted into storage, content intact" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.1 adoption broken (link=$([ -L "$D/data-dir" ] && echo yes || echo no))" | tee -a "$TRANSCRIPT"
fi
# adopted bytes physically live under storage now. Normalize double
# slashes (TMPDIR often ends in /) — wrk cleans paths, the shell does not.
TARGET=$(readlink "$D/data-dir")
S_NORM=$(printf '%s' "$S" | sed 's#//*#/#g')
case "$TARGET" in
  "$S_NORM"/*) _mark_pass; echo "  PASS: L.1 adoption target under scratch storage" | tee -a "$TRANSCRIPT" ;;
  *)           _mark_fail; echo "  FAIL: L.1 adoption target outside storage: $TARGET" | tee -a "$TRANSCRIPT" ;;
esac
# env: create:false with nothing anywhere — link must NOT create it.
if [ ! -e "$D/.env" ]; then
  _mark_pass; echo "  PASS: L.1 create:false resource left absent" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.1 create:false resource materialized" | tee -a "$TRANSCRIPT"
fi
# status: linked + expected are resting states → --exit-code 0.
( cd "$D" && expect_exit 0 "$WRK --storage $S status --exit-code" )
( cd "$D" && expect_contains "expected" "$WRK --storage $S status" )

subsec "L.2: link is idempotent"
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )

subsec "L.3: conflict — local copy AND shared copy both exist"
rm -f "$D/data-dir"                       # drop the symlink...
mkdir -p "$D/data-dir"                    # ...and plant a divergent real copy
echo "local-divergence" > "$D/data-dir/payload.txt"
( cd "$D" && expect_exit 2 "$WRK --storage $S link" )
( cd "$D" && expect_contains "independent copy exists" "$WRK --storage $S link 2>&1" )
# The refusal must not have clobbered either side.
if [ "$(cat "$D/data-dir/payload.txt")" = "local-divergence" ]; then
  _mark_pass; echo "  PASS: L.3 local copy untouched by refused link" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.3 local copy modified by refused link" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_contains "conflict" "$WRK --storage $S status" )
( cd "$D" && expect_exit 1 "$WRK --storage $S status --exit-code" )
rm -rf "$D/data-dir"                      # accept the shared copy
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )

subsec "L.4: absent — no hook, nothing anywhere → refusal + absent state"
D=$SCRATCH/L4
mkrepo git "$D"
seed "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: ghost
    path: ghost-dir
YAML
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/L4
( cd "$D" && expect_exit 2 "$WRK --storage $S link" )
( cd "$D" && expect_contains "no initialize hook" "$WRK --storage $S link 2>&1" )
( cd "$D" && expect_contains "absent" "$WRK --storage $S status" )

subsec "L.5: stale — fingerprint input changed → status flags, link repoints, old variant kept"
D=$SCRATCH/L5
mkrepo git "$D"
seed "$D"
node_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/L5
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
OLD_TARGET=$(readlink "$D/node_modules")
echo '{"v":2}' > "$D/package.json"
( cd "$D" && expect_contains "stale" "$WRK --storage $S status" )
( cd "$D" && expect_exit 1 "$WRK --storage $S status --exit-code" )
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
NEW_TARGET=$(readlink "$D/node_modules")
if [ "$NEW_TARGET" != "$OLD_TARGET" ] && [ -d "$OLD_TARGET" ]; then
  _mark_pass; echo "  PASS: L.5 repointed to new variant, old variant retained" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.5 old=$OLD_TARGET new=$NEW_TARGET" | tee -a "$TRANSCRIPT"
fi

subsec "L.6: user-created symlink → conflict, never overwritten"
D=$SCRATCH/L6
mkrepo git "$D"
seed "$D"
node_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$SCRATCH/L6-external"
ln -s "$SCRATCH/L6-external" "$D/node_modules"
S=$SCRATCH/storage/L6
( cd "$D" && expect_exit 2 "$WRK --storage $S link" )
( cd "$D" && expect_contains "not to shared storage" "$WRK --storage $S link 2>&1" )
if [ "$(readlink "$D/node_modules")" = "$SCRATCH/L6-external" ]; then
  _mark_pass; echo "  PASS: L.6 user symlink preserved" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.6 user symlink was replaced" | tee -a "$TRANSCRIPT"
fi

subsec "L.7: storage deleted out-of-band → stale, link re-provisions via hook"
D=$SCRATCH/L7
mkrepo git "$D"
seed "$D"
node_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/L7
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
rm -rf "$S"          # external interference: whole storage vanishes
( cd "$D" && expect_contains "stale" "$WRK --storage $S status" )
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if [ -f "$D/node_modules/marker.txt" ]; then
  _mark_pass; echo "  PASS: L.7 hook re-provisioned after storage loss" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.7 workspace still dangling after link" | tee -a "$TRANSCRIPT"
fi

subsec "L.8: glob path — every match linked"
D=$SCRATCH/L8
mkrepo git "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: pkgs
    path: packages/*/node_modules
YAML
mkdir -p "$D/packages/a" "$D/packages/b"
printf 'keep\n' > "$D/packages/a/keep" ; printf 'keep\n' > "$D/packages/b/keep"
printf 'demo\n' > "$D/README.md"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/packages/a/node_modules" "$D/packages/b/node_modules"
echo a > "$D/packages/a/node_modules/f"
echo b > "$D/packages/b/node_modules/f"
S=$SCRATCH/storage/L8
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if [ -L "$D/packages/a/node_modules" ] && [ -L "$D/packages/b/node_modules" ]; then
  _mark_pass; echo "  PASS: L.8 both glob matches linked" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: L.8 glob matches not all linked" | tee -a "$TRANSCRIPT"
fi

section M "wrk link — config validation surface"

subsec "M.1: invalid YAML → exit 2"
D=$SCRATCH/M1
mkrepo git "$D"
printf 'resources: [unclosed\n' > "$D/.wrk.yml"
( cd "$D" && expect_exit 2 "$WRK link --dry-run" )

subsec "M.2: duplicate resource names → exit 2 naming the rule"
D=$SCRATCH/M2
mkrepo git "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: dup
    path: a
  - name: dup
    path: b
YAML
( cd "$D" && expect_contains "duplicate name" "$WRK link --dry-run 2>&1" )

subsec "M.3: path escaping the repository → exit 2"
D=$SCRATCH/M3
mkrepo git "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: escape
    path: ../outside
YAML
( cd "$D" && expect_contains "escapes the repository root" "$WRK link --dry-run 2>&1" )

section N "wrk link — managed ignore block"

subsec "N.1: link writes the managed block into .git/info/exclude"
D=$SCRATCH/N1
mkrepo git "$D"
seed "$D"
full_config "$D"
rm -f "$D/.env"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/data-dir"
echo "payload" > "$D/data-dir/payload.txt"
S=$SCRATCH/storage/N1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
EXCLUDE="$D/.git/info/exclude"
if grep -qF "# Added by wrk" "$EXCLUDE" && grep -qF "# End of wrk-managed block" "$EXCLUDE" \
   && grep -qxF "node_modules" "$EXCLUDE" && grep -qxF "data-dir" "$EXCLUDE" \
   && grep -qxF ".wrk.local.yml" "$EXCLUDE"; then
  _mark_pass; echo "  PASS: N.1 managed block present with resource patterns" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: N.1 managed block malformed:" | tee -a "$TRANSCRIPT"
  sed 's/^/    /' "$EXCLUDE" | tee -a "$TRANSCRIPT"
fi
# git must consider the wrk-managed symlinks invisible.
if [ -z "$(cd "$D" && git status --porcelain)" ]; then
  _mark_pass; echo "  PASS: N.1 git status clean after link" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: N.1 git status dirty after link:" | tee -a "$TRANSCRIPT"
  ( cd "$D" && git status --porcelain | sed 's/^/    /' | tee -a "$TRANSCRIPT" )
fi

subsec "N.2: removing a resource prunes its pattern on the next link"
node_config "$D"        # config shrinks to node only
rm -f "$D/data-dir"     # drop the now-unmanaged symlink so link has no opinion
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if grep -qxF "data-dir" "$EXCLUDE"; then
  _mark_fail; echo "  FAIL: N.2 stale pattern data-dir survived" | tee -a "$TRANSCRIPT"
else
  _mark_pass; echo "  PASS: N.2 stale pattern pruned" | tee -a "$TRANSCRIPT"
fi

subsec "N.3: user rules survive; directory-only collision warns and adds alongside"
D=$SCRATCH/N3
mkrepo git "$D"
seed "$D"
node_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
EXCLUDE="$D/.git/info/exclude"
mkdir -p "$(dirname "$EXCLUDE")"
printf 'my-own-rule\nnode_modules/\n' > "$EXCLUDE"
S=$SCRATCH/storage/N3
( cd "$D" && expect_contains "directory-only" "$WRK --storage $S link 2>&1" )
if grep -qxF "my-own-rule" "$EXCLUDE" && grep -qxF "node_modules/" "$EXCLUDE" && grep -qxF "node_modules" "$EXCLUDE"; then
  _mark_pass; echo "  PASS: N.3 user rules kept, slash-less pattern added alongside" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: N.3 exclude content wrong:" | tee -a "$TRANSCRIPT"
  sed 's/^/    /' "$EXCLUDE" | tee -a "$TRANSCRIPT"
fi

subsec "N.4: a bare * glob never sweeps repository infrastructure"
D=$SCRATCH/N4
mkrepo git "$D"
cat > "$D/.wrk.yml" <<'YAML'
resources:
  - name: everything
    path: "*"
YAML
printf 'demo\n' > "$D/README.md"
( cd "$D" && git add -A && git commit -q -m init )
mkdir -p "$D/legit-dir"
echo f > "$D/legit-dir/f"
S=$SCRATCH/storage/N4
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if [ -d "$D/.git" ] && [ ! -L "$D/.git" ] && [ -f "$D/.wrk.yml" ] && [ ! -L "$D/.wrk.yml" ]; then
  _mark_pass; echo "  PASS: N.4 .git and .wrk.yml untouched by * glob" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: N.4 infrastructure was swept into management!" | tee -a "$TRANSCRIPT"
fi
if [ -L "$D/legit-dir" ]; then
  _mark_pass; echo "  PASS: N.4 legitimate glob match still managed" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: N.4 legit match not managed" | tee -a "$TRANSCRIPT"
fi
