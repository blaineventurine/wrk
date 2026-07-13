#!/usr/bin/env bash
# `.wrk.local.yml` — personal additions, same-name overrides, the
# redirect warning, origin visibility, and local-only operation.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

section V "wrk config — .wrk.local.yml overlays"

subsec "V.1: local addition + same-name override with redirect warning"
D=$SCRATCH/V1
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
        - run: sh -c "mkdir -p '{shared}' && echo yarn > '{shared}/tool.txt'"
          cwd: "{root}"
YAML
( cd "$D" && git add -A && git commit -q -m init )
cat > "$D/.wrk.local.yml" <<'YAML'
resources:
  # Same-name override: different path → wrk warns about the redirect.
  - name: node
    path: node_modules_pnpm
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo pnpm > '{shared}/tool.txt'"
          cwd: "{root}"

  # Personal-only addition.
  - name: envrc
    path: .envrc
    create: false
YAML
S=$SCRATCH/storage/V1
# The override redirects the path — the load warning must surface.
( cd "$D" && expect_contains "redirects path" "$WRK --storage $S link 2>&1" )
# The OVERRIDE won: the pnpm path is linked, the shared path is not.
if [ -L "$D/node_modules_pnpm" ] && [ "$(cat "$D/node_modules_pnpm/tool.txt")" = "pnpm" ]; then
  _mark_pass; echo "  PASS: V.1 local override provisioned its own path/hook" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: V.1 override path not linked" | tee -a "$TRANSCRIPT"
fi
if [ ! -e "$D/node_modules" ]; then
  _mark_pass; echo "  PASS: V.1 shared path left alone (entirely replaced by override)" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: V.1 shared path was provisioned despite override" | tee -a "$TRANSCRIPT"
fi

subsec "V.2: status shows the config sources and origins"
( cd "$D" && expect_contains ".wrk.local.yml" "$WRK --storage $S status" )
( cd "$D" && expect_contains "local-override" "$WRK --storage $S status" )
( cd "$D" && expect_contains "envrc" "$WRK --storage $S status" )

subsec "V.3: .wrk.local.yml itself is ignored via the managed block"
if grep -qxF ".wrk.local.yml" "$D/.git/info/exclude"; then
  _mark_pass; echo "  PASS: V.3 local config auto-ignored" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: V.3 local config not in exclude" | tee -a "$TRANSCRIPT"
fi
if [ -z "$(cd "$D" && git status --porcelain | grep -F '.wrk.local.yml')" ]; then
  _mark_pass; echo "  PASS: V.3 git does not see .wrk.local.yml" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: V.3 git sees .wrk.local.yml" | tee -a "$TRANSCRIPT"
fi

subsec "V.4: local-only repo (no .wrk.yml at all)"
D=$SCRATCH/V4
mkrepo git "$D"
printf 'demo\n' > "$D/README.md"
( cd "$D" && git add -A && git commit -q -m init )
cat > "$D/.wrk.local.yml" <<'YAML'
resources:
  - name: data
    path: data-dir
YAML
mkdir -p "$D/data-dir"
echo private > "$D/data-dir/f.txt"
S=$SCRATCH/storage/V4
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
if [ -L "$D/data-dir" ] && [ "$(cat "$D/data-dir/f.txt")" = "private" ]; then
  _mark_pass; echo "  PASS: V.4 local-only config provisions" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: V.4 local-only config did not link" | tee -a "$TRANSCRIPT"
fi

subsec "V.5: no config at all → clear error"
D=$SCRATCH/V5
mkrepo git "$D"
printf 'demo\n' > "$D/README.md"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && expect_exit 2 "$WRK link" )
( cd "$D" && expect_contains ".wrk.yml" "$WRK link 2>&1" )
