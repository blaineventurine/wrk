#!/usr/bin/env bash
# `wrk init` variants.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

section A "wrk init variants"

subsec "A.1: init on empty git repo with project files"
D=$SCRATCH/A1
mkrepo git "$D"
seed "$D"
( cd "$D" && expect_contains "Detected" "$WRK init" )

subsec "A.2: --dry-run does not write"
D=$SCRATCH/A2
mkrepo git "$D"
seed "$D"
( cd "$D" && expect_contains "Would write to" "$WRK init --dry-run" )
if [ -e "$D/.wrk.yml" ]; then
  echo "FAIL: A.2 wrote .wrk.yml despite --dry-run" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  echo "PASS: A.2 no .wrk.yml on disk" | tee -a "$TRANSCRIPT"
  _mark_pass
fi

subsec "A.3: existing config refuses without --force"
D=$SCRATCH/A3
mkrepo git "$D"
seed "$D"
( cd "$D" && "$WRK" init > /dev/null 2>&1 )
( cd "$D" && expect_exit 2 "$WRK init" )

subsec "A.4: --force overwrites (--yes bypasses non-TTY consent)"
( cd "$D" && expect_exit 0 "$WRK init --force --yes" )

subsec "A.5: refuses outside a repo"
D=$SCRATCH/A5
mkdir -p "$D"
( cd "$D" && expect_contains "not inside a Git or Jujutsu" "$WRK init" )

