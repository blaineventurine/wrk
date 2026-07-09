#!/usr/bin/env bash
# `wrk relink --isolate` — promote detached files into a per-workspace
# variant under shared storage. Contract:
#   - After --isolate, the workspace path is a symlink to
#     `<storage>/<repo>/<resource>/isolated-<hex>/`.
#   - --isolate without --yes on a non-TTY refuses (safety gate).
#   - --isolate on a still-linked resource errors ("not detached").

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! "$WRK" relink --help > /dev/null 2>&1; then
  echo "SKIP: wrk relink not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

# Verify the --isolate flag exists — this group is a no-op on older
# binaries that predate Task 3.5. Capture --help output first (rather
# than piping straight into grep) so `grep -q` closing stdin early
# doesn't SIGPIPE the wrk process and trip `set -o pipefail`.
relink_help=$("$WRK" relink --help 2>&1)
if ! printf '%s\n' "$relink_help" | grep -q -- '--isolate'; then
  echo "SKIP: wrk relink --isolate not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

# iso_config writes a single-resource .wrk.yml whose initialize hook
# just marks the shared variant as "installed" — the isolate flow
# doesn't care about hook output, we just need a variant to detach.
iso_config() {
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
          cwd: "{root}"
YAML
}

section I "wrk relink --isolate — per-workspace variant promotion"

subsec "I.1: isolate a detached resource pins the workspace to a private variant"
D=$SCRATCH/I1
mkrepo git "$D"
seed "$D"
iso_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/I1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --isolate --yes" )
# Workspace path should now be a symlink into an isolated-<hex> variant.
target=$(readlink "$D/node_modules" 2>/dev/null || echo NONE)
if [ "$target" != "NONE" ] && echo "$target" | grep -q 'isolated-'; then
  _mark_pass
  printf '  PASS: symlink target = %s\n' "$target" | tee -a "$TRANSCRIPT"
else
  _mark_fail
  printf '  FAIL: expected symlink into isolated-<hex>, got: %s\n' "$target" | tee -a "$TRANSCRIPT"
fi

subsec "I.2: --isolate without --yes on a non-TTY refuses"
D=$SCRATCH/I2
mkrepo git "$D"
seed "$D"
iso_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/I2
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
# expect_contains runs the command through eval; stdin is inherited
# from the harness's non-TTY stdin, so the shared Confirm helper's
# non-TTY refusal path fires without needing an explicit </dev/null.
( cd "$D" && expect_contains "requires --yes" "$WRK --storage $S relink --isolate" )

subsec "I.3: --isolate on a linked (non-detached) resource errors"
D=$SCRATCH/I3
mkrepo git "$D"
seed "$D"
iso_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/I3
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
# No detach — resource is still linked. Naming it explicitly means the
# preflight loop reaches the "not detached" branch instead of the
# empty-names "no detached resources to isolate" branch.
( cd "$D" && expect_contains "not detached" "$WRK --storage $S relink --isolate --yes node" )
