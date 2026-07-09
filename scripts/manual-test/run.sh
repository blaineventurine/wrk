#!/usr/bin/env bash
# Orchestrator for the wrk manual functional test harness.
#
# Usage:
#   ./run.sh                    # run every group under groups/
#   ./run.sh init               # run only groups/init.sh
#   ./run.sh init new detach    # run listed groups in order
#
# Environment:
#   WRK        — path to the wrk binary (default: ./bin/wrk, then /tmp/wrk)
#   SCRATCH    — scratch directory (default: fresh mktemp)
#   KEEP=1     — do not remove SCRATCH on exit

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"

# ---------------------------------------------------------------------------
# Resolve the binary under test.
# ---------------------------------------------------------------------------

if [ -z "${WRK:-}" ]; then
  for candidate in "$PWD/bin/wrk" /tmp/wrk; do
    if [ -x "$candidate" ]; then
      WRK="$candidate"
      break
    fi
  done
fi

if [ -z "${WRK:-}" ] || [ ! -x "$WRK" ]; then
  echo "run.sh: cannot locate a wrk binary." >&2
  echo "  Build one with: go build -o ./bin/wrk ./cmd/wrk" >&2
  echo "  Or set WRK=/path/to/wrk before invoking this script." >&2
  exit 2
fi

export WRK

# ---------------------------------------------------------------------------
# Set up scratch tree + transcript.
# ---------------------------------------------------------------------------

if [ -z "${SCRATCH:-}" ]; then
  SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/wrk-manual-test.XXXXXX")"
fi
export SCRATCH

TRANSCRIPT="$SCRATCH/transcript.log"
export TRANSCRIPT
: > "$TRANSCRIPT"

cleanup() {
  if [ "${KEEP:-0}" != "1" ]; then
    rm -rf "$SCRATCH"
  else
    echo
    echo "SCRATCH kept at: $SCRATCH"
  fi
}

# Counter files live at the top-level scratch so every group appends
# to the same tally. Groups get a per-group SCRATCH below.
export PASS_FILE="$SCRATCH/.pass-count"
export FAIL_FILE="$SCRATCH/.fail-count"
touch "$PASS_FILE" "$FAIL_FILE"
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Discover groups.
# ---------------------------------------------------------------------------

if [ "$#" -eq 0 ]; then
  # Run every script under groups/, sorted. mapfile requires bash 4;
  # macOS ships bash 3, so use IFS=newline instead. Group filenames
  # must not contain whitespace.
  IFS=$'\n' read -r -d '' -a groups < <(
    find "$HERE/groups" -maxdepth 1 -type f -name '*.sh' | sort
    printf '\0'
  )
else
  groups=()
  for name in "$@"; do
    path="$HERE/groups/$name.sh"
    if [ ! -f "$path" ]; then
      echo "run.sh: no such group: $name (expected $path)" >&2
      exit 2
    fi
    groups+=("$path")
  done
fi

if [ "${#groups[@]}" -eq 0 ]; then
  echo "run.sh: no groups to run (looked under $HERE/groups)" >&2
  exit 2
fi

# ---------------------------------------------------------------------------
# Metadata header in the transcript.
# ---------------------------------------------------------------------------

{
  printf 'wrk manual functional test transcript\n'
  printf '  date:      %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '  wrk:       %s\n' "$WRK"
  printf '  version:   %s\n' "$("$WRK" --version 2>/dev/null || echo unknown)"
  printf '  git:       %s\n' "$(git --version 2>/dev/null || echo missing)"
  printf '  jj:        %s\n' "$(jj --version 2>/dev/null || echo missing)"
  printf '  scratch:   %s\n' "$SCRATCH"
  printf '  groups:    %s\n' "$(basename -s .sh "${groups[@]}" | paste -sd' ' -)"
} | tee -a "$TRANSCRIPT"

# ---------------------------------------------------------------------------
# Run each group in its own subshell so exports don't bleed.
# ---------------------------------------------------------------------------

total_pass=0
total_fail=0

for group in "${groups[@]}"; do
  name="$(basename -s .sh "$group")"
  echo | tee -a "$TRANSCRIPT"
  printf '########## group: %s ##########\n' "$name" | tee -a "$TRANSCRIPT"

  (
    # Give each group its own sub-scratch so `$SCRATCH/A1` in one
    # group doesn't collide with `$SCRATCH/A1` in another.
    export SCRATCH="$SCRATCH/$name"
    export WRK TRANSCRIPT PASS_FILE FAIL_FILE
    mkdir -p "$SCRATCH"
    bash "$group"
  )
done

total_pass=$(wc -c < "$SCRATCH/.pass-count" 2>/dev/null | tr -d ' ')
total_fail=$(wc -c < "$SCRATCH/.fail-count" 2>/dev/null | tr -d ' ')
total_pass=${total_pass:-0}
total_fail=${total_fail:-0}

echo | tee -a "$TRANSCRIPT"
printf '########## summary ##########\n' | tee -a "$TRANSCRIPT"
printf 'total assertions: %d pass, %d fail\n' "$total_pass" "$total_fail" | tee -a "$TRANSCRIPT"
printf 'transcript: %s\n' "$TRANSCRIPT" | tee -a "$TRANSCRIPT"

if [ "$total_fail" -gt 0 ]; then
  exit 1
fi
