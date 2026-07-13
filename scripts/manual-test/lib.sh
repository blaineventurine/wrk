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
  local ec out err

  out="$(mktemp)"
  err="$(mktemp)"
  printf '$ %s   # expect exit=%d\n' "$cmd" "$expected" | tee -a "$TRANSCRIPT"
  eval "$cmd" > "$out" 2> "$err"
  ec=$?
  if [ "$ec" -eq "$expected" ]; then
    _mark_pass
    printf '  PASS (exit=%d)\n' "$ec" | tee -a "$TRANSCRIPT"
  else
    _mark_fail
    printf '  FAIL (expected exit=%d, got %d)\n' "$expected" "$ec" | tee -a "$TRANSCRIPT"
    if [ -s "$out" ]; then
      printf '  stdout:\n' | tee -a "$TRANSCRIPT"
      sed 's/^/    /' "$out" | tee -a "$TRANSCRIPT"
    fi
    if [ -s "$err" ]; then
      printf '  stderr:\n' | tee -a "$TRANSCRIPT"
      sed 's/^/    /' "$err" | tee -a "$TRANSCRIPT"
    fi
  fi
  rm -f "$out" "$err"
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

# expect_json <kind> <cmd...> — run cmd and record PASS/FAIL based on
# its STDOUT being a single, pure JSON envelope of the given kind:
#   1. stdout is non-empty and its first byte is '{' (purity — no
#      human chatter, VCS output, or plan text may precede it),
#   2. it parses as JSON (via python3 when available; skipped
#      otherwise), and
#   3. it carries "kind": "<kind>" and "schema": 1.
# stderr is deliberately ignored: progress noise may go there.
expect_json() {
  local kind="$1"; shift
  local cmd="$*"
  local out ec

  out="$(mktemp)"
  printf '$ %s   # expect pure JSON envelope kind=%s\n' "$cmd" "$kind" | tee -a "$TRANSCRIPT"
  eval "$cmd" > "$out" 2> /dev/null
  ec=$?

  local first
  first="$(head -c 1 "$out")"
  if [ "$first" != "{" ]; then
    _mark_fail
    printf '  FAIL (stdout does not start with "{" — envelope impure or missing)\n' | tee -a "$TRANSCRIPT"
    printf '  stdout:\n' | tee -a "$TRANSCRIPT"
    sed 's/^/    /' "$out" | tee -a "$TRANSCRIPT"
    rm -f "$out"
    return "$ec"
  fi

  if command -v python3 > /dev/null 2>&1; then
    if ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$out" 2> /dev/null; then
      _mark_fail
      printf '  FAIL (stdout is not valid JSON)\n' | tee -a "$TRANSCRIPT"
      sed 's/^/    /' "$out" | tee -a "$TRANSCRIPT"
      rm -f "$out"
      return "$ec"
    fi
  fi

  if grep -qF "\"kind\": \"$kind\"" "$out" && grep -qF '"schema": 1' "$out"; then
    _mark_pass
    printf '  PASS (kind=%s, schema=1, pure stdout)\n' "$kind" | tee -a "$TRANSCRIPT"
  else
    _mark_fail
    printf '  FAIL (kind/schema not found in envelope)\n' | tee -a "$TRANSCRIPT"
    sed 's/^/    /' "$out" | tee -a "$TRANSCRIPT"
  fi
  rm -f "$out"
  return "$ec"
}

# expect_stderr_code <code> <cmd...> — run cmd expecting exit 2, an
# EMPTY stdout, and a structured error envelope on stderr carrying the
# given stable error code. This is the `--json` failure contract.
expect_stderr_code() {
  local code="$1"; shift
  local cmd="$*"
  local out err ec

  out="$(mktemp)"
  err="$(mktemp)"
  printf '$ %s   # expect exit=2, empty stdout, stderr error code=%s\n' "$cmd" "$code" | tee -a "$TRANSCRIPT"
  eval "$cmd" > "$out" 2> "$err"
  ec=$?

  local ok=1
  [ "$ec" -eq 2 ] || { ok=0; printf '  exit=%d, want 2\n' "$ec" | tee -a "$TRANSCRIPT"; }
  [ ! -s "$out" ] || { ok=0; printf '  stdout not empty\n' | tee -a "$TRANSCRIPT"; }
  grep -qF "\"code\":\"$code\"" "$err" || grep -qF "\"code\": \"$code\"" "$err" || {
    ok=0; printf '  stderr missing error code %s\n' "$code" | tee -a "$TRANSCRIPT"
  }

  if [ "$ok" -eq 1 ]; then
    _mark_pass
    printf '  PASS (exit=2, clean stdout, code=%s)\n' "$code" | tee -a "$TRANSCRIPT"
  else
    _mark_fail
    printf '  FAIL\n' | tee -a "$TRANSCRIPT"
    printf '  stdout:\n' | tee -a "$TRANSCRIPT"; sed 's/^/    /' "$out" | tee -a "$TRANSCRIPT"
    printf '  stderr:\n' | tee -a "$TRANSCRIPT"; sed 's/^/    /' "$err" | tee -a "$TRANSCRIPT"
  fi
  rm -f "$out" "$err"
  return "$ec"
}
