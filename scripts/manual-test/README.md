# wrk manual functional test harness

Semi-automated end-to-end tests exercising a real `wrk` binary against
synthetic git / jj / colocated repos.

## When to use

- **Before a release** — walk every command against fresh fixtures.
- **After a load-bearing change** — the group covering the changed
  subsystem catches integration bugs the Go test suite can miss.
- **Reproducing a user report** — add a scenario to the relevant group,
  re-run, capture the output.

The Go test suite is authoritative for behavior. This harness is for
integration and UX — it exercises the compiled binary against real
`git worktree add` / `jj workspace add` invocations, real hooks, real
symlinks, real config parsing.

## Layout

```
scripts/manual-test/
├── README.md            # this file
├── lib.sh               # shared helpers (mkrepo, seed, run, subsec, ...)
├── run.sh               # orchestrator: pick a group or run all
└── groups/
    ├── init.sh          # `wrk init` variants
    ├── new.sh           # `wrk new` edge cases
    ├── link.sh          # `wrk link` core + config errors
    ├── detach-relink.sh # `wrk detach` / `wrk relink`
    ├── status.sh        # `wrk status` / `wrk workspaces`
    ├── fingerprint.sh   # fingerprint variant behaviour
    ├── interference.sh  # external interference (worktree remove, hacks)
    ├── jj.sh            # jj-specific paths
    ├── concurrency.sh   # cross-workspace concurrency
    ├── errors.sh        # error surface
    ├── local.sh         # .wrk.local.yml scenarios
    ├── gc.sh            # `wrk gc` (once implemented)
    ├── remove.sh        # `wrk remove` (once implemented)
    └── forget.sh        # `wrk forget` (once implemented)
```

## Prerequisites

- A built `wrk` binary. The harness looks for `$WRK` (default
  `./bin/wrk`, then `/tmp/wrk`).
- `git` on `PATH`.
- `jj` on `PATH` for the jj groups (they skip cleanly if missing).

Build the binary once:

```sh
go build -o ./bin/wrk ./cmd/wrk
```

## Running

```sh
# Run every group.
./scripts/manual-test/run.sh

# Run a single group.
./scripts/manual-test/run.sh init
./scripts/manual-test/run.sh new detach-relink

# Keep the scratch tree for post-mortem instead of tearing it down.
KEEP=1 ./scripts/manual-test/run.sh gc

# Override the binary under test.
WRK=/tmp/wrk-experimental ./scripts/manual-test/run.sh
```

Each run writes a transcript to `$SCRATCH/transcript.log`. The
default `$SCRATCH` is a fresh `mktemp -d`; override to reuse a
directory or capture across runs.

## Writing a scenario

Every group is a self-contained shell script that sources `lib.sh` and
uses its helpers:

```sh
#!/usr/bin/env bash
set -uo pipefail
. "$(dirname "$0")/../lib.sh"

section "X" "wrk foo edge cases"

subsec "X.1: happy path"
D=$SCRATCH/X1
mkrepo git "$D"
seed "$D"
writeconfig "$D"
( cd "$D" && run "$WRK foo bar" )

subsec "X.2: refusal"
D=$SCRATCH/X2
mkrepo git "$D"
seed "$D"
( cd "$D" && run "$WRK foo" )
```

Helpers:

| helper | purpose |
|--------|---------|
| `mkrepo <kind> <path>` | `kind` = `git`, `colocated`, `pure-jj` |
| `seed <path>` | Common ignored files: `package.json`, `Gemfile`, `.env`, ... |
| `writeconfig <path>` | Committed `.wrk.yml` with two fingerprinted resources + hooks |
| `run <cmd>` | Runs, records exit + stdout + stderr into the transcript |
| `section <letter> <title>` | Top-level heading |
| `subsec <title>` | Sub-heading |
| `expect_exit <expected> <cmd>` | Asserts a specific exit code, records pass/fail |
| `expect_contains <needle> <cmd>` | Asserts stdout/stderr contains a substring |

`expect_*` helpers turn scenarios from "human eyeballs the transcript"
into "harness exits non-zero on regression". Prefer them for stable
invariants (exit codes, load-bearing strings). Fall back to plain `run`
for output whose exact shape may drift.

## Adding a new group

1. Create `groups/<name>.sh` following the template above.
2. Make it executable: `chmod +x groups/<name>.sh`.
3. `run.sh` auto-discovers scripts under `groups/` — no registration
   needed.

## Cleaning up

By default `run.sh` removes `$SCRATCH` on exit (both success and
failure). Override with `KEEP=1` to inspect the fixture tree.
