#!/usr/bin/env bash
# `wrk remove` — feature workspace removal.

set -uo pipefail
. "$(dirname "$0")/../lib.sh"

if ! "$WRK" remove --help > /dev/null 2>&1; then
  echo "SKIP: wrk remove not implemented in this binary" | tee -a "$TRANSCRIPT"
  exit 0
fi

# remove_config writes a minimal .wrk.yml with no resources, sufficient for
# wrk new to succeed without fingerprinting or provisioning.
remove_config() {
  local root="$1"
  cat > "$root/.wrk.yml" <<'YAML'
resources: []
YAML
}

section E "wrk remove — happy path"

subsec "E.1: wrk remove <feature> --yes tears down the feature (bare name)"
D=$SCRATCH/E1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new e1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/e1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: E.1 wrk new did not create $FEATURE" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_exit 0 "$WRK remove e1feature --yes" )
  if [ -e "$FEATURE" ]; then
    echo "FAIL: E.1 feature dir survived remove" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: E.1 feature dir gone after remove" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "E.2: wrk remove with absolute path argument"
D=$SCRATCH/E2
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new e2feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/e2feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: E.2 wrk new did not create $FEATURE" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_exit 0 "$WRK remove $FEATURE --yes" )
  if [ -e "$FEATURE" ]; then
    echo "FAIL: E.2 feature dir survived remove (absolute-path)" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: E.2 absolute-path remove worked" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "E.3: --dry-run prints plan without touching disk"
D=$SCRATCH/E3
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new e3feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/e3feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: E.3 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_contains "Removing workspace" "$WRK remove e3feature --dry-run" )
  if [ ! -d "$FEATURE" ]; then
    echo "FAIL: E.3 --dry-run deleted the feature dir" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: E.3 --dry-run left disk untouched" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

section F "wrk remove — refusals"

subsec "F.1: target = primary (invoked from feature) → refuse with 'primary'"
D=$SCRATCH/F1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new f1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/f1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: F.1 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Invoke from the feature workspace, remove primary by absolute path.
  ( cd "$FEATURE" && expect_contains "primary" "$WRK remove $D --yes 2>&1" )
fi

subsec "F.2: target = current workspace → refuse with 'current'"
D=$SCRATCH/F2
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new f2feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/f2feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: F.2 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Invoke from the feature workspace targeting itself.
  ( cd "$FEATURE" && expect_contains "current" "$WRK remove $FEATURE --yes 2>&1" )
fi

subsec "F.3: non-TTY without --yes → refuse (exit non-zero)"
D=$SCRATCH/F3
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new f3feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/f3feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: F.3 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && "$WRK" remove f3feature < /dev/null > /dev/null 2>&1 )
  ec=$?
  if [ "$ec" = "0" ]; then
    echo "FAIL: F.3 non-TTY without --yes exited 0" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: F.3 non-TTY without --yes refused (exit=$ec)" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

section G "wrk remove — ghost redirect"

subsec "G.1: ghost with stranded registry entry → refuse with 'wrk gc' hint"
D=$SCRATCH/G1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new g1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/g1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: G.1 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Seed a detach-registry entry so the ghost hint fires. The registry
  # lives at <git-common-dir>/wrk/detached.json (git backend). We use
  # `git rev-parse --git-common-dir` for portability.
  COMMON_DIR=$( cd "$D" && git rev-parse --git-common-dir 2>/dev/null )
  # git rev-parse may return a relative path; resolve it against the primary.
  case "$COMMON_DIR" in
    /*) ;;
    *)  COMMON_DIR="$D/$COMMON_DIR" ;;
  esac
  REG_DIR="$COMMON_DIR/wrk"
  mkdir -p "$REG_DIR"
  # Canonicalize the feature path to match what wrk canonicalizes at plan time.
  CANON_FEATURE=$(cd "$FEATURE" && pwd -P)
  printf '{%s:["node_modules"]}' "\"$CANON_FEATURE\"" > "$REG_DIR/detached.json"

  # Hard-remove the worktree dir so `git worktree list` marks it prunable.
  rm -rf "$FEATURE"

  # wrk remove <ghost> should refuse with a wrk gc hint.
  ( cd "$D" && expect_contains "wrk gc" "$WRK remove g1feature 2>&1" )
fi

section J "wrk remove — jj colocated backend"

# Skip the whole section when jj is unavailable — CI containers
# without jj should not fail this group.
if ! command -v jj > /dev/null 2>&1; then
  echo "SKIP: jj not available on PATH; skipping section J" | tee -a "$TRANSCRIPT"
else

subsec "J.1: jj colocated repo — wrk remove deletes the workspace directory"
D=$SCRATCH/J1
mkrepo colocated "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new j1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/j1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: J.1 wrk new did not create $FEATURE" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  ( cd "$D" && expect_exit 0 "$WRK remove j1feature --yes" )
  # Directory MUST be gone — the bug this fix targets left it on disk.
  if [ -e "$FEATURE" ]; then
    echo "FAIL: J.1 feature dir survived remove on jj backend" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: J.1 jj feature dir gone after remove" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
  # jj must no longer list the workspace either.
  if ( cd "$D" && jj workspace list 2>/dev/null | grep -q j1feature ); then
    echo "FAIL: J.1 jj still lists forgotten workspace" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: J.1 jj workspace list no longer surfaces j1feature" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

subsec "J.2: jj colocated repo with uncommitted changes — wrk remove refuses without --force"
D=$SCRATCH/J2
mkrepo colocated "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new j2feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/j2feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: J.2 setup failed" | tee -a "$TRANSCRIPT"
  _mark_fail
else
  # Untracked file — jj diff --summary emits one line for the new
  # file in the @ change vs its parent.
  printf 'dirty\n' > "$FEATURE/uncommitted.txt"

  # Refusal path: plan surfaces "uncommitted" and the CLI exits
  # non-zero without --force.
  ( cd "$D" && expect_contains "uncommitted" "$WRK remove j2feature --yes 2>&1" )
  if [ ! -d "$FEATURE" ]; then
    echo "FAIL: J.2 refusal path still tore down the feature dir" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: J.2 refusal path left disk untouched" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi

  # --force overrides the refusal and the directory is finally gone.
  ( cd "$D" && expect_exit 0 "$WRK remove j2feature --yes --force" )
  if [ -e "$FEATURE" ]; then
    echo "FAIL: J.2 --force did not sweep the feature dir" | tee -a "$TRANSCRIPT"
    _mark_fail
  else
    echo "PASS: J.2 --force swept the feature dir" | tee -a "$TRANSCRIPT"
    _mark_pass
  fi
fi

fi  # jj availability guard

section T "wrk remove — soft refusals (git backend)"

# t_config writes a single hook-provisioned resource so detach/isolate
# refusals have something to register.
t_config() {
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
YAML
}

subsec "T.1: uncommitted changes → refuse; --force overrides"
D=$SCRATCH/T1
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new t1feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/t1feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: T.1 setup failed" | tee -a "$TRANSCRIPT"; _mark_fail
else
  printf 'dirty\n' > "$FEATURE/uncommitted.txt"
  ( cd "$D" && expect_contains "uncommitted" "$WRK remove t1feature --yes 2>&1" )
  if [ -d "$FEATURE" ]; then
    _mark_pass; echo "  PASS: T.1 refusal left the worktree" | tee -a "$TRANSCRIPT"
  else
    _mark_fail; echo "  FAIL: T.1 refusal still removed" | tee -a "$TRANSCRIPT"
  fi
  ( cd "$D" && expect_exit 0 "$WRK remove t1feature --yes --force" )
  if [ ! -e "$FEATURE" ]; then
    _mark_pass; echo "  PASS: T.1 --force removed the dirty worktree" | tee -a "$TRANSCRIPT"
  else
    _mark_fail; echo "  FAIL: T.1 --force did not remove" | tee -a "$TRANSCRIPT"
  fi
fi

subsec "T.2: detached files → refuse naming the copies; --force overrides"
D=$SCRATCH/T2/main
mkdir -p "$D"
mkrepo git "$D"
seed "$D"
t_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/T2
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S new t2feature" )
FEATURE=$SCRATCH/T2/t2feature
( cd "$FEATURE" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$D" && expect_contains "independent local copies" "$WRK --storage $S remove t2feature --yes 2>&1" )
if [ -d "$FEATURE/node_modules" ]; then
  _mark_pass; echo "  PASS: T.2 refusal preserved the detached copy" | tee -a "$TRANSCRIPT"
else
  _mark_fail; echo "  FAIL: T.2 detached copy gone after refusal" | tee -a "$TRANSCRIPT"
fi
( cd "$D" && expect_exit 0 "$WRK --storage $S remove t2feature --yes --force" )

subsec "T.3: isolated variants → refuse; --force orphans them for gc"
D=$SCRATCH/T3/main
mkdir -p "$D"
mkrepo git "$D"
seed "$D"
t_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
S=$SCRATCH/storage/T3
( cd "$D" && expect_exit 0 "$WRK --storage $S link" )
( cd "$D" && expect_exit 0 "$WRK --storage $S new t3feature" )
FEATURE=$SCRATCH/T3/t3feature
( cd "$FEATURE" && expect_exit 0 "$WRK --storage $S detach --yes" )
( cd "$FEATURE" && expect_exit 0 "$WRK --storage $S relink --isolate --yes" )
ISO_TARGET=$(readlink "$FEATURE/node_modules")
( cd "$D" && expect_contains "isolated" "$WRK --storage $S remove t3feature --yes 2>&1" )
( cd "$D" && expect_exit 0 "$WRK --storage $S remove t3feature --yes --force" )
# The orphaned isolated variant is now gc's to sweep.
( cd "$D" && expect_contains "$(basename "$ISO_TARGET")" "$WRK --storage $S gc --dry-run" )

subsec "T.4: invoked from INSIDE the target (subdirectory) → hard refusal"
D=$SCRATCH/T4
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new t4feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/t4feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: T.4 setup failed" | tee -a "$TRANSCRIPT"; _mark_fail
else
  mkdir -p "$FEATURE/deep/inside"
  ( cd "$FEATURE/deep/inside" && expect_exit 2 "$WRK remove $FEATURE --yes" )
  ( cd "$FEATURE/deep/inside" && expect_contains "currently inside" "$WRK remove $FEATURE --yes 2>&1" )
  if [ -d "$FEATURE" ]; then
    _mark_pass; echo "  PASS: T.4 tree survived the inside-invocation (jj backend would have rm -rf'd it pre-fix)" | tee -a "$TRANSCRIPT"
  else
    _mark_fail; echo "  FAIL: T.4 tree deleted despite refusal" | tee -a "$TRANSCRIPT"
  fi
fi

subsec "T.5: uncommitted-changes probe failure → refuse rather than guess clean"
D=$SCRATCH/T5
mkrepo git "$D"
remove_config "$D"
( cd "$D" && git add -A && git commit -q -m init )
( cd "$D" && "$WRK" new t5feature > /dev/null 2>&1 )
FEATURE=$SCRATCH/t5feature
if [ ! -d "$FEATURE" ]; then
  echo "FAIL: T.5 setup failed" | tee -a "$TRANSCRIPT"; _mark_fail
else
  # Corrupt the worktree's gitdir pointer: `git worktree list` still
  # reports the worktree live, but `git status` inside it fails —
  # exactly the unverifiable state a stale jj workspace produces.
  printf 'gitdir: /nonexistent/place\n' > "$FEATURE/.git"
  ( cd "$D" && expect_contains "could not verify" "$WRK remove t5feature --yes 2>&1" )
  if [ -d "$FEATURE" ]; then
    _mark_pass; echo "  PASS: T.5 unverifiable workspace kept on disk" | tee -a "$TRANSCRIPT"
  else
    _mark_fail; echo "  FAIL: T.5 removed a workspace it could not verify" | tee -a "$TRANSCRIPT"
  fi
fi
