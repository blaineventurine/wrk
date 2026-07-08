#!/usr/bin/env bash
# Shared helpers for wrk manual functional testing.
#
# Every scenario script sources this file. `run.sh` invokes each group
# in a subshell with SCRATCH, WRK, and TRANSCRIPT preset.

set -uo pipefail

: "${SCRATCH:?SCRATCH must be set (usually by run.sh)}"
: "${WRK:?WRK must be set (path to the wrk binary)}"
: "${TRANSCRIPT:=$SCRATCH/transcript.log}"

# Counter files. Subshells cannot mutate parent-shell variables, so we
# use files that any process in this scratch tree can append to.
: "${PASS_FILE:=$SCRATCH/.pass-count}"
: "${FAIL_FILE:=$SCRATCH/.fail-count}"
touch "$PASS_FILE" "$FAIL_FILE"

_mark_pass() { printf '.' >> "$PASS_FILE"; }
_mark_fail() { printf '.' >> "$FAIL_FILE"; }

# ---------------------------------------------------------------------------
# Fixture helpers
# ---------------------------------------------------------------------------

# mkrepo <kind> <path> — create a fresh repo of the given kind.
# kind = git | colocated | pure-jj
mkrepo() {
  local kind="$1"
  local path="$2"

  mkdir -p "$path"
  (
    cd "$path" || exit 1
    case "$kind" in
      git)
        git init -q -b main
        git config user.email t@t
        git config user.name t
        ;;
      colocated)
        git init -q -b main
        git config user.email t@t
        git config user.name t
        jj git init --colocate
        ;;
      pure-jj)
        jj git init
        ;;
      *)
        echo "mkrepo: unknown kind: $kind" >&2
        return 1
        ;;
    esac
  )
}

# seed <path> — populate a repo with common ignored resources so wrk has
# something to detect / manage.
seed() {
  local path="$1"
  (
    cd "$path" || exit 1
    printf '{"name":"demo","version":"1.0.0","dependencies":{"lodash":"4.17.21"}}\n' > package.json
    printf 'source "https://rubygems.org"\ngem "rails"\n' > Gemfile
    printf 'GEM\n  remote: https://rubygems.org/\n  specs:\n    rails (7.0.0)\n' > Gemfile.lock
    printf '3.2.0\n' > .ruby-version
    printf '' > .env
    printf 'export FOO=bar\n' > .envrc
    printf '# demo\n' > README.md
    git add -A 2>/dev/null || true
    git commit -q -m init 2>/dev/null || true
  )
}

# writeconfig <path> — install a rich .wrk.yml with two fingerprinted
# resources plus two out-of-band ones. Suitable for exercising every
# state machine transition.
writeconfig() {
  local path="$1"
  cat > "$path/.wrk.yml" <<'YAML'
resources:
  - name: env
    path: .env
    create: false

  - name: envrc
    path: .envrc
    create: false

  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}' && echo v1 > '{shared}/.installed' && mkdir -p '{shared}/lodash' && echo lodash-v1 > '{shared}/lodash/index.js'"
          cwd: "{root}"

  - name: bundler
    path: vendor/bundle
    fingerprint:
      - "{root}/Gemfile"
      - "{root}/Gemfile.lock"
      - "{root}/.ruby-version"
    hooks:
      initialize:
        - run: sh -c "mkdir -p '{shared}/ruby/3.2.0/gems' && echo installed > '{shared}/.installed'"
          cwd: "{root}"
YAML
}

# ---------------------------------------------------------------------------
# Transcript helpers
# ---------------------------------------------------------------------------

# section <letter> <title>
section() {
  local letter="$1"; shift
  local title="$*"
  printf '\n\n=== %s. %s ===\n' "$letter" "$title" | tee -a "$TRANSCRIPT"
}

# subsec <title>
subsec() {
  printf '\n--- %s ---\n' "$*" | tee -a "$TRANSCRIPT"
}

# run <cmd> — execute cmd, record exit/stdout/stderr in the transcript,
# return cmd's exit code.
run() {
  local cmd="$*"
  printf '$ %s\n' "$cmd" | tee -a "$TRANSCRIPT"

  local out err
  out="$(mktemp)"
  err="$(mktemp)"
  eval "$cmd" > "$out" 2> "$err"
  local ec=$?

  printf 'exit=%d\n' "$ec" | tee -a "$TRANSCRIPT"
  if [ -s "$out" ]; then
    printf 'stdout:\n' | tee -a "$TRANSCRIPT"
    sed 's/^/  /' "$out" | tee -a "$TRANSCRIPT"
  fi
  if [ -s "$err" ]; then
    printf 'stderr:\n' | tee -a "$TRANSCRIPT"
    sed 's/^/  /' "$err" | tee -a "$TRANSCRIPT"
  fi
  rm -f "$out" "$err"
  return $ec
}

# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------

# expect_exit <expected> <cmd...> — run cmd and record PASS/FAIL based
# on exit code match.
expect_exit() {
  local expected="$1"; shift
  local cmd="$*"
  local ec

  printf '$ %s   # expect exit=%d\n' "$cmd" "$expected" | tee -a "$TRANSCRIPT"
  eval "$cmd" > /tmp/wrk-mt-out 2> /tmp/wrk-mt-err
  ec=$?
  if [ "$ec" -eq "$expected" ]; then
    _mark_pass
    printf '  PASS (exit=%d)\n' "$ec" | tee -a "$TRANSCRIPT"
  else
    _mark_fail
    printf '  FAIL (expected exit=%d, got %d)\n' "$expected" "$ec" | tee -a "$TRANSCRIPT"
    if [ -s /tmp/wrk-mt-out ]; then
      printf '  stdout:\n' | tee -a "$TRANSCRIPT"
      sed 's/^/    /' /tmp/wrk-mt-out | tee -a "$TRANSCRIPT"
    fi
    if [ -s /tmp/wrk-mt-err ]; then
      printf '  stderr:\n' | tee -a "$TRANSCRIPT"
      sed 's/^/    /' /tmp/wrk-mt-err | tee -a "$TRANSCRIPT"
    fi
  fi
  rm -f /tmp/wrk-mt-out /tmp/wrk-mt-err
  return "$ec"
}

# expect_contains <needle> <cmd...> — run cmd and record PASS/FAIL based
# on whether the combined stdout+stderr contains the given substring.
expect_contains() {
  local needle="$1"; shift
  local cmd="$*"
  local combined

  printf '$ %s   # expect output contains %q\n' "$cmd" "$needle" | tee -a "$TRANSCRIPT"
  combined="$(eval "$cmd" 2>&1)"
  if printf '%s' "$combined" | grep -qF -- "$needle"; then
    _mark_pass
    printf '  PASS\n' | tee -a "$TRANSCRIPT"
  else
    _mark_fail
    printf '  FAIL (needle not found in output)\n' | tee -a "$TRANSCRIPT"
    printf '  output:\n' | tee -a "$TRANSCRIPT"
    printf '%s\n' "$combined" | sed 's/^/    /' | tee -a "$TRANSCRIPT"
  fi
}
