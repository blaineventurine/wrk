#!/usr/bin/env bash
# `--json` — the agent contract: every supporting command emits ONE pure
# JSON envelope on stdout (schema=1, kind=<command>); failures emit a
# structured error envelope on stderr with a stable code and exit 2.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

json_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo v1 > '{shared}/content.txt'"
          cwd: "{root}"
YAML
}

section J "--json envelopes (read-only + destructive + creators)"

# One linked fixture serves every read-only envelope probe.
D=$SCRATCH/J0
mkrepo git "$D"
seed "$D"
json_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/J0
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )

subsec "J.1: read-only envelopes — status, list, workspaces, fingerprint, doctor"
( cd "$D" && expect_json status      "$WRK --storage $S status --json" )
( cd "$D" && expect_json status      "$WRK --storage $S status --all --json" )
( cd "$D" && expect_json list        "$WRK --storage $S list --json" )
( cd "$D" && expect_json list        "$WRK --storage $S list --size --json" )
( cd "$D" && expect_json workspaces  "$WRK --storage $S workspaces --json" )
( cd "$D" && expect_json fingerprint "$WRK --storage $S fingerprint node --json" )
( cd "$D" && expect_json doctor      "$WRK --storage $S doctor --json" )

subsec "J.2: destructive envelopes — dry-run previews carry the plan, no result"
( cd "$D" && expect_json detach "$WRK --storage $S detach --json --dry-run" )
( cd "$D" && expect_json relink "$WRK --storage $S relink --json --dry-run" )
( cd "$D" && expect_json gc     "$WRK --storage $S gc --json --dry-run" )
( cd "$D" && expect_json forget "$WRK --storage $S forget --json --dry-run" )
( cd "$D" && expect_json run    "$WRK --storage $S run node --json --dry-run" )

subsec "J.3: destructive envelopes — executed with --yes carry a result"
( cd "$D" && expect_json detach "$WRK --storage $S detach --json --yes" )
( cd "$D" && expect_json relink "$WRK --storage $S relink --json --yes" )
( cd "$D" && expect_json run    "$WRK --storage $S run node --json --yes" )
( cd "$D" && expect_json gc     "$WRK --storage $S gc --json --yes" )

subsec "J.4: creator envelopes — wrk new / wrk init"
( cd "$D" && expect_json new "$WRK --storage $S new j4-feature --json" )
F=$SCRATCH/j4-feature
if [ -d "$F" ] && [ -L "$F/node_modules" ]; then
  _mark_pass; echo "  PASS: J.4 new --json created and provisioned the workspace" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: J.4 workspace missing after new --json" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_json new "$WRK --storage $S new j4-preview --json --dry-run" )
if [ ! -e "$SCRATCH/j4-preview" ]; then
  _mark_pass; echo "  PASS: J.4 new --json --dry-run created nothing" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: J.4 dry-run created a workspace" | tee -a "$TRANSCRIPT"
fi
D2=$SCRATCH/J4-init
mkrepo git "$D2"
printf '{"name":"j4"}\n' > "$D2/package.json"
printf '' > "$D2/yarn.lock"
( cd "$D2" && expect_json init "$WRK init --json --dry-run" )
if [ ! -e "$D2/.wrk.yml" ]; then
  _mark_pass; echo "  PASS: J.4 init --json --dry-run wrote nothing" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: J.4 init dry-run wrote .wrk.yml" | tee -a "$TRANSCRIPT"
fi
( cd "$D2" && expect_json init "$WRK init --json" )
if [ -f "$D2/.wrk.yml" ]; then
  _mark_pass; echo "  PASS: J.4 init --json wrote the config" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: J.4 init --json did not write" | tee -a "$TRANSCRIPT"
fi

subsec "J.5: failure contract — stderr envelope, stable code, clean stdout"
# Unknown resource → resource_not_configured.
( cd "$D" && expect_stderr_code resource_not_configured "$WRK --storage $S run nonexistent --json --dry-run" )
# Prompting command under --json without consent → json_requires_yes.
( cd "$D" && expect_stderr_code json_requires_yes "$WRK --storage $S relink --json" )
# init overwrite with --force but no --yes → json_requires_yes.
( cd "$D2" && expect_stderr_code json_requires_yes "$WRK init --json --force" )
# Outside any repository → structured error (not_a_repository), exit 2.
D3=$SCRATCH/J5-norepo
mkdir -p "$D3"
( cd "$D3" && expect_stderr_code not_a_repository "$WRK --storage $S gc --json --dry-run" )

subsec "J.6: gc --exit-code taxonomy — 1 when there is work, 0 when clean"
D=$SCRATCH/J6
mkrepo git "$D"
seed "$D"
json_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/J6
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S gc --dry-run --exit-code" )
# Manufacture sweepable work: flip the fingerprint so the old variant unpins.
echo '{"v":2}' > "$D/package.json"
rm -f "$D/node_modules"
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 1 "$WRK --storage $S gc --dry-run --exit-code" )
