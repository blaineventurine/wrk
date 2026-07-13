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
├── lib.sh               # shared helpers (mkrepo, seed, run, expect_*, ...)
├── run.sh               # orchestrator: pick a group or run all
└── groups/
    ├── init.sh          # `wrk init` variants
    ├── new.sh           # `wrk new` — --base, refusals
    ├── link.sh          # `wrk link` core: adoption, conflicts, states,
    │                    #   config validation, managed ignore block
    ├── detach-relink.sh # `wrk detach` / `wrk relink`, incl. the
    │                    #   isolation exit (plain relink un-isolates)
    ├── isolate.sh       # `wrk relink --isolate` entry path
    ├── run.sh           # `wrk run` — hook re-runs, crash recovery,
    │                    #   detached/isolated refusals
    ├── multiworkspace.sh# variant coexistence, gc pin survival,
    │                    #   flock race, storage-side glob provisioning
    ├── gc.sh            # `wrk gc` — variants, ghosts, bookkeeping
    ├── gc-orphans.sh    # `wrk gc` — orphaned-storage sweep
    ├── remove.sh        # `wrk remove` — refusals (git + jj), ghosts
    ├── forget.sh        # `wrk forget`
    ├── jj.sh            # jj backend: colocation requirement, lifecycle,
    │                    #   exclude-file contract
    ├── local.sh         # `.wrk.local.yml` overlays and origins
    └── json.sh          # `--json` envelope + error-code contract
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
| `expect_json <kind> <cmd>` | Asserts stdout is ONE pure JSON envelope of the given kind (schema=1) |
| `expect_stderr_code <code> <cmd>` | Asserts exit 2, empty stdout, and a structured stderr error envelope with the stable code |

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
