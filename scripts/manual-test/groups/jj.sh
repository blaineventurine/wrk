#!/usr/bin/env bash
# Jujutsu backend — colocation requirement, colocated lifecycle, and
# the exclude-file contract (`jj status` must not see managed paths).

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! command -v jj > /dev/null 2>&1; then
  echo "SKIP: jj not available on PATH" | tee -a "$TRANSCRIPT"
  exit 0
fi

jj_config() {
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

section P "jj backend — non-colocated repos"

subsec "P.1: pure (non-colocated) jj repo — coherent either way"
# Historical jj versions fail `jj git root` on non-colocated repos, so
# wrk refuses with a "colocated" hint. Modern jj exposes the internal
# git store, so wrk works end-to-end. Both are acceptable; what is NOT
# acceptable is a third state (crash, partial link, exit 1).
D=$SCRATCH/P1
mkrepo pure-jj "$D"
jj_config "$D"
printf '{"name":"p1"}\n' > "$D/package.json"
S=$SCRATCH/storage/P1
( cd "$D" && "$WRK" --storage "$S" link > /dev/null 2>&1 )
ec=$?
if [ "$ec" = "0" ]; then
  # Modern-jj branch: the link must be fully coherent.
  if [ -L "$D/node_modules" ] && [ -f "$D/node_modules/content.txt" ]; then
    _mark_pass; echo "  PASS: P.1 pure-jj link works end-to-end on this jj ($(jj --version 2>/dev/null))" | tee -a "$TRANSCRIPT"
  else
    _mark_fail; echo "  FAIL: P.1 link exited 0 but the workspace is not linked" | tee -a "$TRANSCRIPT"
  fi
  ( cd "$D" && expect_exit 0 "$WRK --storage $S status --exit-code" )
elif [ "$ec" = "2" ]; then
  # Historical-jj branch: the refusal must name the remedy.
  ( cd "$D" && expect_contains "colocated" "$WRK --storage $S link 2>&1" )
else
  _mark_fail; echo "  FAIL: P.1 unexpected exit $ec from link on pure-jj repo" | tee -a "$TRANSCRIPT"
fi

section Q "jj backend — colocated lifecycle"

subsec "Q.1: link + status on a colocated repo"
D=$SCRATCH/Q1
mkrepo colocated "$D"
seed "$D"
jj_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/Q1
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_contains "linked" "$WRK --storage $S status" )
( cd "$D" && expect_exit 0 "$WRK --storage $S status --exit-code" )

subsec "Q.2: jj status does not see managed paths (exclude honored)"
JJ_OUT=$( cd "$D" && jj status 2>/dev/null )
if printf '%s' "$JJ_OUT" | grep -q "node_modules"; then
  _mark_fail; echo "  FAIL: Q.2 jj status sees node_modules:" | tee -a "$TRANSCRIPT"
  printf '%s\n' "$JJ_OUT" | sed 's/^/    /' | tee -a "$TRANSCRIPT"
else
  _mark_pass; echo "  PASS: Q.2 managed symlink invisible to jj status" | tee -a "$TRANSCRIPT"
fi
# Positive control: a real new file IS visible.
printf 'x\n' > "$D/visible.txt"
JJ_OUT=$( cd "$D" && jj status 2>/dev/null )
if printf '%s' "$JJ_OUT" | grep -q "visible.txt"; then
  _mark_pass; echo "  PASS: Q.2 positive control visible to jj status" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Q.2 positive control not visible — exclude check is vacuous" | tee -a "$TRANSCRIPT"
fi
rm -f "$D/visible.txt"

subsec "Q.3: wrk new creates and provisions a jj workspace"
( cd "$D" && expect_exit 0 "$WRK --storage $S new q3-feature" )
F=$SCRATCH/q3-feature
if [ -d "$F" ] && [ -L "$F/node_modules" ] && [ -f "$F/node_modules/content.txt" ]; then
  _mark_pass; echo "  PASS: Q.3 jj workspace created and linked" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Q.3 jj workspace missing or unlinked at $F" | tee -a "$TRANSCRIPT"
fi
if ( cd "$D" && jj workspace list 2>/dev/null | grep -q "q3-feature" ); then
  _mark_pass; echo "  PASS: Q.3 jj workspace list shows the new workspace" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: Q.3 jj does not list the new workspace" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_contains "q3-feature" "$WRK --storage $S workspaces" )

subsec "Q.4: detach → isolate → relink exit on the jj backend"
( cd "$D" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --isolate --yes" )
ISO_TARGET=$(readlink "$D/node_modules")
case "$ISO_TARGET" in
  *isolated-*) _mark_pass; echo "  PASS: Q.4 isolated on jj backend" | tee -a "$TRANSCRIPT" ;;
  *)           _mark_fail; echo "  FAIL: Q.4 not isolated: $ISO_TARGET" | tee -a "$TRANSCRIPT" ;;
esac
( cd "$D" && expect_exit 0 "$WRK --storage $S relink --yes" )
NEW_TARGET=$(readlink "$D/node_modules")
case "$NEW_TARGET" in
  *isolated-*) _mark_fail; echo "  FAIL: Q.4 relink did not exit isolation" | tee -a "$TRANSCRIPT" ;;
  *)
    if [ ! -e "$ISO_TARGET" ] && [ -f "$D/node_modules/content.txt" ]; then
      _mark_pass; echo "  PASS: Q.4 isolation exit on jj: reconnected, variant deleted" | tee -a "$TRANSCRIPT"
    else
      _mark_fail; echo "  FAIL: Q.4 exit incomplete (variant=$([ -e "$ISO_TARGET" ] && echo present || echo gone))" | tee -a "$TRANSCRIPT"
    fi
    ;;
esac

subsec "Q.5: gc on a colocated repo — both workspaces pin their variant"
( cd "$D" && expect_contains "Nothing to do" "$WRK --storage $S gc --dry-run" )
