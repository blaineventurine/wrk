# wrk

Provision shared resources across Jujutsu workspaces and Git worktrees.

`wrk` manages ignored resources such as `.env`, `node_modules`, and
`vendor/bundle`, allowing multiple workspaces to share them without
duplicating installations.

It never modifies tracked project files or repository history.

---

## Motivation

Modern projects often rely on large ignored resources:

- `.env`
- `node_modules`
- `vendor/bundle`
- `.venv`
- build caches

Every new workspace normally requires recreating those resources.

`wrk` stores them once and links them into every workspace.

For dependency installations, `wrk` fingerprints the dependency manifests
so multiple versions can safely coexist.

---

## How it works

```
                 Workspace A
              ┌──────────────┐
              │ node_modules │
              └──────┬───────┘
                     │
                     │ symlink
                     ▼
   ~/Library/Application Support/wrk/
       repositories/
           github.com/
               my-org/
                   project/
                       node_modules/
                           5fd1d0d610ba6c17/
                     ▲
                     │
                     │ symlink
              ┌──────┴───────┐
              │ node_modules │
              └──────────────┘
                 Workspace B
```

The first workspace initializes the shared resource.

Every subsequent workspace simply links to it.

---

## Features

- Share files and directories across workspaces.
- Fingerprint resources using one or more files.
- Automatic workspace provisioning.
- Automatic repository preparation.
- Works with Jujutsu workspaces and Git worktrees.
- Read-only introspection (`status`, `workspaces`, `list`).
- Dry-run mode.
- Reclaim disk and tear down workspaces (`wrk gc`, `wrk remove`, `wrk forget`).
- Fully reversible (`wrk detach`) with an explicit reconnect (`wrk relink`).

---

## Installation

The tool is currently in private beta. If you have access to the
repository, follow [`docs/INSTALL.md`](./docs/INSTALL.md).

---

## Configuration

Create a `.wrk.yml` in the repository root — either by hand, or by running
`wrk init` (see [Bootstrapping](#bootstrapping)) to generate one from the
project files it detects (`package.json`, `Gemfile`, `pyproject.toml`, …).

```yaml
resources:
  - name: env
    path: .env

  - name: bundler
    path: vendor/bundle

    fingerprint:
      - "{root}/Gemfile"
      - "{root}/Gemfile.lock"

    hooks:
      initialize:
        - run: bundle install
          cwd: "{root}"
          env:
            BUNDLE_PATH: "{shared}"
```

### Local overrides

You can create a `.wrk.local.yml` alongside `.wrk.yml` to add or override
resources for your own machine. It is never committed — `wrk` maintains a
managed block in the repository-local exclude file (`.git/info/exclude`,
shared by colocated Jujutsu repos) covering it and every configured
resource path. The block is rewritten on each `wrk link`, so patterns for
resources you remove from the config are pruned automatically; your own
rules outside the block are never touched.

**Adding a personal-only resource:**

```yaml
# .wrk.local.yml
resources:
  - name: envrc
    path: .envrc
    create: false
```

**Overriding a shared resource by name:**

```yaml
# .wrk.local.yml
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
      - "{root}/pnpm-lock.yaml"
    hooks:
      initialize:
        - run: pnpm install
          cwd: "{root}"
```

Local entries with the same `name` as a shared entry replace it entirely
(they do not deep-merge). Entries with a new `name` are additions.

The resource's origin is visible in `wrk status` and `wrk list` as `shared`,
`local`, or `local-override`.

Additional examples are available in the [`examples/`](./examples/) directory:

- `examples/basic/` — minimal `.env` sharing
- `examples/node/` — fingerprinted `node_modules` with Yarn
- `examples/rails/` — fingerprinted `vendor/bundle`
- `examples/local-override/` — using `.wrk.local.yml` to swap yarn → pnpm locally

See [`examples/local-override/`](./examples/local-override/) for a complete
worked example.

> `.wrk.yml` is optional if `.wrk.local.yml` is present. If you're using
> `wrk` for a personal project without any team-shared config, you can
> keep everything in `.wrk.local.yml` alone.

### Resource fields

| Field | Type | Description |
|---|---|---|
| `name` | string | Human-readable identifier used in output. |
| `path` | string | Workspace-relative path of the resource (file or directory). May be a glob pattern (`packages/*/node_modules`): every match becomes its own instance, and matches that peer workspaces have already provisioned into shared storage are linked even in a fresh workspace where the paths don't exist on disk yet. Repository infrastructure (`.git`, `.jj`, the wrk config files) and wrk's reserved scratch names are never matched, no matter what the pattern says. |
| `fingerprint` | list | Optional. Files whose contents determine which shared variant is used. See [Fingerprinting](#fingerprinting). |
| `hooks.initialize` | list | Optional. Commands run once to create the shared resource. See [Command execution](#command-execution). |
| `create` | bool | Optional; defaults to `true`. When `false`, `wrk` treats a missing resource as **intentional** — nothing is provisioned, no hook runs, and `wrk status` reports it as `expected` instead of `absent`. Use this when the file is provided by an external tool (a secrets manager, `direnv`, `1Password`, `sops`) rather than by wrk. |

### Command execution

Hook `run` strings are **tokenized**, not interpreted by a shell.

`wrk` splits `run` into arguments using shell-like word splitting (quotes and
escapes are respected) and executes the command directly. It does **not**
invoke `sh -c`, so shell-only features are unavailable:

| Not supported in `run`      | Use instead                                       |
|-----------------------------|---------------------------------------------------|
| `FOO=bar cmd`               | the `env:` map                                    |
| `cmd-a && cmd-b`            | separate `run:` entries, or an explicit shell     |
| `cmd \| other`, `cmd > file`| an explicit shell                                 |
| `$VAR` expansion            | placeholders (`{root}`, `{shared}`, …) or `env:`  |

Set environment variables with `env:`:

```yaml
hooks:
  initialize:
    - run: bundle install
      cwd: "{root}"
      env:
        BUNDLE_PATH: "{shared}"
```

Run multiple steps as separate entries (each runs in order, and any failure
aborts the hook):

```yaml
hooks:
  initialize:
    - run: yarn install --frozen-lockfile
      cwd: "{root}"
    - run: yarn build
      cwd: "{root}"
```

If you genuinely need shell features, invoke a shell explicitly:

```yaml
hooks:
  initialize:
    - run: sh -c "yarn install && yarn build"
      cwd: "{root}"
```

Placeholders are expanded in `run`, `cwd`, and every `env` value before
`run` is tokenized.

---

## Resource lifecycle

For every configured resource:

```
Shared resource exists?

    Yes
        │
        ▼
    Link into workspace.

    No
        │
        ▼
    Run initialize hook (if configured).
        │
        ▼
    Link into workspace.
```

Resources without an `initialize` hook are expected to already exist.

```yaml
resources:
  - name: env
    path: .env
```

`wrk` will never create `.env`. If it is missing everywhere, `wrk
status` reports it as `absent` and `wrk link` fails with a conflict —
wrk has no way to produce it and no reason to believe you have another
source.

### `create: false` — provided out-of-band

`create: false` tells `wrk` that a missing resource is intentional:
the file is provided by something outside `wrk`'s scope (a secrets
manager, `direnv`, `1Password`, `sops`, a build-time template).

```yaml
resources:
  - name: env
    path: .env
    create: false
```

With this set:

| State | Behavior |
|-------|----------|
| File missing everywhere | `wrk status` reports `expected` (not `absent`), `wrk link` skips it, `wrk status --exit-code` treats it as healthy. |
| File appears in one workspace | The next `wrk link` adopts it into shared storage and every workspace links to the same copy. |
| File already in shared storage | New workspaces get a symlink; no external tool is re-invoked. |

Use `create: false` for anything `wrk` cannot produce but MUST share
once someone else drops it into place. Leave `create` unset (defaults
to `true`) when `wrk` is responsible for the resource — either via an
`initialize` hook or by adopting an existing workspace copy.

On the other hand:

```yaml
resources:
  - name: bundler
    path: vendor/bundle

    hooks:
      initialize:
        - run: bundle install
          cwd: "{root}"
          env:
            BUNDLE_PATH: "{shared}"
```

If the shared bundle does not exist, `wrk` runs the initialize hook once.

Subsequent workspaces simply reuse the shared copy.

---

## Commands

### Bootstrapping

Generate a starter `.wrk.yml` for the current repository:

```bash
wrk init
```

`wrk init` inspects the current directory for well-known project files
(`package.json` + `yarn.lock`/`pnpm-lock.yaml`/`bun.lockb`/`package-lock.json`
including monorepo `workspaces` layouts, `Gemfile`, `pyproject.toml` +
`uv.lock`/`poetry.lock`, `Pipfile.lock`, `requirements.txt`, `Cargo.toml`,
`.env.example`/`.env.sample`) and writes a `.wrk.yml` seeded with sensible
defaults for each detected layout. It refuses to overwrite an existing
`.wrk.yml` unless you pass `--force`; overwriting prompts for
confirmation on an interactive terminal (skip with `--yes`), and
`--dry-run` prints the generated file to stdout without touching disk.

```bash
wrk init --dry-run       # preview only
wrk init --force         # overwrite an existing .wrk.yml (prompts; --yes skips)
wrk init --json          # machine-readable envelope (see Machine-readable output)
```

With nothing detected, `wrk init` still writes a commented template so you
can uncomment the resources you actually need. Edit the result, then run
`wrk link` to provision.

### Provisioning

Initialize or repair the current workspace:

```bash
wrk link
```

Preview the planned actions:

```bash
wrk link --dry-run
```

Create and provision a new workspace as a sibling of the current one:

```bash
wrk new feature-auth
```

A bare name is placed next to the current workspace, so `wrk new feature-auth`
from `/proj/main` lands at `/proj/feature-auth` — no need to type `../` every
time. Explicit relative paths (`./foo`, `../foo`, `foo/bar`) and absolute paths
are resolved literally, so long-standing habits like `wrk new ../feature-auth`
still work.

`wrk new` bases the new worktree on the CURRENT worktree's HEAD (or `@`, for
Jujutsu). Run it from any worktree — primary or secondary — and the new
workspace forks off that worktree's state. Previously implicit; now called
out explicitly so `wrk new` from within a secondary worktree does what you
expect.

Use `--base <ref>` to fork off a specific branch, tag, or commit instead:

```bash
wrk new feature --base main
wrk new hotfix --base v1.0.0
wrk new experiment --base abc1234
```

On Git, `--base <ref>` creates a new branch named after the destination path
off `<ref>` — so `wrk new feature --base main` lands on a fresh `feature`
branch forked from `main`. On Jujutsu, the new workspace's `@` starts on top
of `<ref>`.

`wrk` refuses to create a new workspace inside any existing one. Nested
worktrees confuse both Git and Jujutsu, and wrk's shared-storage links assume
workspace roots are siblings — never parents or children of one another. The
check runs before the underlying `git worktree add` / `jj workspace add`, so
an illegal destination never creates an orphan branch.

Preview what `wrk new` would do without creating anything:

```bash
wrk new feature-auth --dry-run
```

The primary workspace's link plan is printed, along with the resolved
destination. Nothing is written and no `git worktree add` / `jj workspace add`
runs — safe to try when the destination or nesting rules are unclear.

Add `--json` for a machine-readable envelope (`kind: "new"`): the plan
carries the resolved destination, base, and the primary link plan; the
result reports `created` and `workspaceRoot`. The underlying
`git worktree add` / `jj workspace add` chatter is captured into the
envelope's `warnings` array so stdout stays pure JSON.

### Independence and reconnection

Detach the current workspace from shared resources (creates independent
local copies):

```bash
wrk detach
```

Reconnect the current workspace to shared storage, **discarding** the
independent copies created by `detach`:

```bash
wrk relink
```

`wrk relink` is also the exit from isolation: any resource pinned to a
private `isolated-*` variant via `wrk relink --isolate` is reconnected
to its regular fingerprint variant (provisioning it if needed) and the
isolated variant is **deleted**. The plan lists each exit with a ⚠
before you confirm. Isolation entries whose resource is no longer in
`.wrk.yml` are left untouched and called out in the plan.

> `wrk link` is intentionally conservative: it will never destroy a local
> copy. Use `wrk relink` when you explicitly want to throw away independent
> work and reconnect to shared storage. Destructive actions are flagged in
> the plan output with a ⚠ warning.

### Cleanup

Reclaim disk, tear down worktrees, or forget a repository entirely. All three
commands print a plan first, prompt for confirmation on an interactive
terminal, and refuse without `--yes` when stdin isn't a TTY. Add `--dry-run`
to preview without executing.

#### `wrk gc` — reclaim disk

```bash
wrk gc                # plan → confirm → execute
wrk gc --yes          # skip the prompt (e.g. cron / CI)
wrk gc --dry-run      # show the plan, do nothing
```

`wrk gc` runs four sweeps against the current repository:

- **Ghost workspaces** — worktrees whose working directory is gone but
  VCS metadata still references them. `wrk gc` runs `git worktree prune`
  or `jj workspace forget` for each ghost. After this step, subsequent
  operations see a consistent view of live workspaces.
- **Unused fingerprint variants** — variant subdirectories under
  `<storage>/<repo-id>/<resource>/` that aren't symlinked from any live
  workspace. A variant is considered *in use* if a live workspace's
  resource path resolves under it — in this clone **or in any other
  registered clone** of the repository (every `wrk link` registers its
  clone in a storage-side registry, so independent clones sharing the
  same remote cannot sweep each other's pins; a clone that cannot be
  enumerated makes the sweep fully conservative). Detached workspaces
  don't pin their previous variant. Concurrent `wrk link` operations
  are respected: a variant whose lock is currently held is skipped
  with a warning.
- **Orphaned storage** — subtrees under `<storage>/<repo-id>/` that no
  live workspace's configuration claims: the leftovers of resources
  removed or renamed in `.wrk.yml`. Every live workspace's own config
  (including `.wrk.local.yml`) is consulted, and isolated variants stay
  pinned via the isolation registry. If any workspace is unreachable or
  its config is unreadable, this sweep is skipped with a note rather
  than guessed at, and each deletion re-checks its claim at execute
  time.
- **Stale bookkeeping** — orphaned `.wrk-lock` files, `.wrk-provisioning`
  scratch dirs whose lock is free, `.wrk-deleting` markers left by a
  previous crashed `wrk gc`, and `.wrk-forgetting/` markers left by a
  previous crashed `wrk forget`.

Storage-side deletes use rename-then-remove: a variant is renamed to
`<variant>.wrk-deleting/` before `RemoveAll` runs, so a crash mid-delete
leaves a marker the next `wrk gc` sweeps up.

#### `wrk remove <workspace>` — tear down a worktree

```bash
wrk remove feature-auth       # bare name, resolved as a sibling of the primary
wrk remove ../feature-auth    # explicit relative path
wrk remove /abs/path/to/wt    # absolute path
wrk remove feature-auth --yes # skip the prompt
```

`wrk remove` combines `git worktree remove` (or `jj workspace forget`)
with cleanup of the workspace's detach-registry entry, so `wrk status
--all` stays consistent afterwards.

Hard refusals (cannot be overridden):

- The primary workspace of the repository.
- The workspace the command is currently running from.

Soft refusals (overridable with `--force`):

- Uncommitted VCS changes in the target.
- The uncommitted-changes probe itself failing (e.g. a stale Jujutsu
  workspace that needs `jj workspace update-stale`, or broken worktree
  metadata) — wrk refuses to guess that an unverifiable workspace is
  clean.
- Detached files (`wrk detach` was run in the target — the independent
  local copies would be lost).
- Isolated variants (`wrk relink --isolate` was run in the target — the
  private variants would become unreferenced and swept by the next
  `wrk gc`).

Ghost workspaces route the user to `wrk gc`:

```
$ wrk remove old-feature
Refusing: old-feature is not a live workspace; VCS metadata and/or a
detach registry entry still reference it. Run 'wrk gc' to sweep the ghost.
```

#### `wrk forget` — remove all wrk state for the current repo

```bash
wrk forget            # plan → confirm → execute
wrk forget --yes      # skip the prompt
wrk forget --dry-run  # show what would be removed
```

`wrk forget` deletes the entire `<storage>/<repo-id>/` subtree (every
variant of every resource) and clears the detach registry for this repo.
`.wrk.yml`, working files, and VCS metadata are left untouched.

After `wrk forget`, a subsequent `wrk link` re-provisions from scratch —
new fingerprints are computed against the current manifests, initialize
hooks re-run, symlinks are re-created.

`wrk forget` refuses without `--force` when any workspace has detached
files or isolated variants, or when **another clone** of the repository
is registered against the same storage: forgetting shared storage while
independent copies exist would leave those workspaces stranded, isolated
variants hold per-workspace content that hooks cannot reproduce, and
other clones' workspaces would all dangle at once. Run
`wrk relink --yes` in each affected workspace first, or pass `--force`
to proceed anyway.

Removal is atomic: `<storage>/<repo-id>/` is renamed to
`<storage>/<repo-id>.wrk-forgetting/` (single rename), then removed.
A crash between the rename and the registry clear leaves the marker;
the next `wrk forget` or `wrk gc` sweeps it automatically.

### Introspection (read-only)

Show the state of every managed resource in the current workspace:

```bash
wrk status
```

Show state across every workspace of the repository:

```bash
wrk status --all
```

Exit non-zero if any resource needs user attention (any state except
`linked`, `expected`, or `detached` — see [Resource states](#resource-states)
below). Useful in CI or pre-commit hooks:

```bash
wrk status --exit-code
```

Exit codes are distinguishable: **1** means "resources need attention" (the
table already told you which ones), **2** means "wrk itself couldn't run"
(bad flags, no repository, config error). CI scripts can treat 1 as an
expected outcome and 2 as a real failure to investigate.

Show every workspace and its overall state:

```bash
wrk workspaces
```

Example output:

```
  WORKSPACE                              STATE      RESOURCES
* /Users/blaine/repos/monolith           linked     2 linked
  /Users/blaine/repos/monolith-feature   detached   2 detached
  /Users/blaine/repos/monolith-hotfix    partial    1 linked, 1 detached
  /Users/blaine/repos/monolith-wip       unhealthy  1 linked, 1 conflict
```

### Resource states

Every row of `wrk status` reports one of these per-resource states:

| State | Meaning | What to do |
|---|---|---|
| `linked` | The workspace path is a symlink pointing at the correct shared copy. | Nothing — this is the healthy state. |
| `expected` | Nothing exists locally, and the resource is `create: false`. Provided out-of-band (e.g. `direnv`, a secrets manager). | Nothing — the missing file is intentional. |
| `detached` | You ran `wrk detach`; the workspace path is now an independent local copy. Recorded on purpose. | `wrk link` to reunite with shared storage (keeping local edits requires manual merge), or `wrk relink` to discard local edits and reconnect. |
| `isolated` | You ran `wrk relink --isolate`; the workspace path is a symlink into a per-workspace variant that no fingerprint maps to. Peer workspaces don't see its content. | `wrk relink` — reconnects to the regular fingerprint variant and **deletes** the isolated copy (confirm-gated). `wrk link` and `wrk detach` leave isolated resources untouched. |
| `pending` | Nothing exists yet, but an `initialize` hook is configured. | `wrk link` — the hook runs, then the symlink is installed. |
| `missing` | The shared copy exists but the workspace symlink is not in place. | `wrk link`. |
| `not-linked` | A real local copy exists but no shared copy yet. | `wrk link` — the local copy moves into shared storage and a symlink takes its place. |
| `stale` | The workspace *is* a symlink, but it points at the wrong shared target. Almost always because a fingerprint input (e.g. `package.json`, `Gemfile.lock`) changed since the last `wrk link`, so a new shared variant now applies. A symlink you created yourself (pointing outside wrk's storage) also reports as `stale`. | `wrk link` — the symlink is repointed to the current variant. For a self-made symlink, `wrk link` refuses with a conflict instead of overwriting it. |
| `conflict` | Both a real local copy AND a shared copy exist. `wrk link` refuses to clobber either. | Decide which one is authoritative. Delete the workspace copy and run `wrk link` to accept the shared copy, or run `wrk detach` to accept the workspace copy and keep it independent. |
| `absent` | Nothing exists anywhere, no `initialize` hook is configured, and `create` is `true`. wrk has no way to produce it. | Provide the file, add a hook, or (if the file is provided externally) set `create: false`. |

`wrk status --exit-code` treats every state except `linked`, `expected`, `detached`, and `isolated` as a problem — those four are stable resting states, everything else needs attention.

Workspace states are a rollup of the [resource states](#resource-states) above:

| State | Meaning |
|---|---|
| `linked` | Every resource is `linked` or `expected`. Nothing to do. |
| `detached` | Every resource is `detached`. |
| `isolated` | Every resource is `isolated`. |
| `partial` | A mix of `linked`, `detached`, and `isolated` resources — deliberate mid-state. |
| `pending` | At least one resource is `pending` (waiting for its initialize hook). Otherwise healthy. |
| `unhealthy` | At least one resource needs user action — `conflict`, `stale`, `missing`, `not-linked`, or `absent`. |

List configured resources and their shared storage:

```bash
wrk list
```

Include on-disk size of each resource (slower — walks the storage tree):

```bash
wrk list --size
```

#### `wrk fingerprint <resource>` — why is this resource stale?

Prints the current fingerprint computed from the resource's inputs
alongside the fingerprint currently pinned by the workspace symlink.
When they differ, run `wrk link` to re-point the workspace at the
current variant. Read-only.

```bash
wrk fingerprint node        # human-readable
wrk fingerprint node --json # machine-readable
```

Example output:

```
Resource:   node (node_modules)
Fingerprint inputs:
  package.json              exists   234 B
  yarn.lock                 exists   45678 B

Current variant:  5fd1d0d610ba6c17
Pinned variant:   8a71d8b219fd0031  (stale)

Run `wrk link` to re-point this workspace at the current variant.
```

#### `wrk doctor` — repository health snapshot

Composes config validation, ghost-workspace detection, and
stale-bookkeeping detection into one summary. Read-only. Exits **0**
by default, whether or not the report is clean; pass `--exit-code`
to have it exit **1** when issues are found (**2** stays reserved for
wrk itself failing).

```bash
wrk doctor              # human-readable, always exits 0 unless wrk itself broke
wrk doctor --json       # machine-readable
wrk doctor --exit-code  # exit 1 when issues found; use in CI
```

Example output:

```
Repository: /Users/me/repos/monolith (git)
  Config:            valid
  Ghost workspaces:  none
  Bookkeeping cruft: none
  Storage size:      1.2 GB

Overall: healthy
```

### Exit codes

wrk uses a small, stable exit-code taxonomy so scripts and agents can
route on outcomes reliably:

| Code | Meaning |
|------|---------|
| 0    | Success |
| 1    | Soft signal: `--exit-code` flag detected a non-empty state (e.g. `wrk status --exit-code` on an unhealthy workspace, `wrk gc --exit-code` when there is cleanup to do, `wrk doctor --exit-code` when issues were found) |
| 2    | wrk itself couldn't complete — bad flags, missing repository, config error, hook failure, permission denied, etc. |

Only `wrk status`, `wrk gc`, and `wrk doctor` currently support
`--exit-code`. Other commands use exit 0 for success and 2 for
failure.

Agents pairing with `wrk` in CI or scripting contexts should treat 1
as an actionable soft signal (something to investigate but not a wrk
failure) and 2 as a hard failure to alert on.

### Machine-readable output (`--json`)

Every read-only introspection command, every destructive command, and
the workspace/config creators (`wrk new`, `wrk init`) support `--json`.
Output is a single JSON object with:

- `schema` — integer version (currently `1`)
- `kind` — command name (`status`, `list`, `fingerprint`, `doctor`, `workspaces`, `gc`, `remove`, `forget`, `run`, `relink`, `relink-isolate`, `detach`, `new`, `init`)
- Command-specific top-level fields

Destructive commands additionally carry:

- `dryRun` — boolean
- `plan` — the full plan structure
- `result` — populated after execute (`null` in `--dry-run` mode). Fields: `attempted: bool`, `bytesFreed: int64`, `warnings: string[]`. For `wrk remove` on the git backend, `bytesFreed` reports the plan's pre-computed size (git deletes inside its own process, so wrk cannot measure the sweep directly).

`wrk new` and `wrk init` create rather than destroy; their `result`
carries command-specific fields instead: `created`/`workspaceRoot`
(new) and `wrote` (init), plus the shared `warnings` array. In
`--dry-run` mode the `result` key is omitted entirely.

Errors under `--json` are emitted on STDERR (STDOUT stays clean) as:

```json
{"error": {"code": "resource_not_configured", "message": "...", "hint": "..."}}
```

Stable error codes agents can switch on:

| Code | Meaning |
|------|---------|
| `resource_not_configured`   | Named resource is not in `.wrk.yml` |
| `resource_not_fingerprinted` | Resource exists but has no `fingerprint` block |
| `resource_no_hook`          | Resource has no `initialize` hook |
| `resource_detached`         | Resource is detached in this workspace |
| `resource_not_detached`     | Resource is currently linked, not detached |
| `resource_isolated`         | Resource is isolated in this workspace (`wrk run` cannot refresh a private variant) |
| `primary_workspace`         | Target is the primary workspace (hard refusal) |
| `current_workspace`         | Target is the workspace the caller is inside |
| `not_live_workspace`        | Target is not a live workspace of this repo |
| `uncommitted_changes`       | Workspace has uncommitted VCS changes (soft refusal) |
| `detached_files`            | Workspace has detached-file registry entries (soft refusal) |
| `plan_conflict`             | Plan actions collide |
| `hook_command_failed`       | A hook command exited non-zero |
| `config_invalid`            | `.wrk.yml` failed to load or validate |
| `not_a_repository`          | Current directory is not inside a supported VCS repo |
| `confirm_declined`          | Interactive prompt got "no" (or EOF without `--yes`/`--force`) |
| `json_requires_yes`         | `--json` on a prompting command needs `--yes` (or `--force --yes` for `wrk init` overwrites) |
| `unknown`                   | Fallback for any untyped error |

Combined `--json --yes` (with `--force` where required) is the agent
contract: emit machine-readable output, skip interactive prompts.

### Global options

Override automatic repository detection:

```bash
wrk --vcs git new feature
wrk --vcs jj  new feature
```

Use a different storage location:

```bash
wrk link --storage /path/to/storage
```

---

## Placeholders

| Placeholder | Description |
|--------------|-------------|
| `{root}` | Repository root |
| `{parent}` | Parent directory of the matched resource |
| `{match}` | Matched workspace path |
| `{shared}` | Shared storage path |

Unknown placeholders in **fingerprint** entries (typos like `{shred}`
for `{shared}`) are rejected when the resource is resolved, so a
misspelled fingerprint input never silently changes the cache key.
Hook fields (`run`, `cwd`, and `env` values) are currently expanded
leniently: an unknown placeholder passes through to the hook as
literal text, so double-check spelling there.

### `{shared}` in initialize hooks

`{shared}` expands to a target path that **may not exist yet** when the
hook runs. Tools that manage their own output directory
(`bundle install`, `pip`, `npm ci`, ...) create it automatically:

```yaml
hooks:
  initialize:
    - run: bundle install
      env:
        BUNDLE_PATH: "{shared}"
```

If your hook writes to `{shared}` directly (e.g. shell redirects),
create it first:

```yaml
hooks:
  initialize:
    - run: sh -c 'mkdir -p "{shared}" && cp source "{shared}/data"'
```

`wrk` runs the hook against a scratch path and only renames it into
place on success, so a failed hook leaves no partial output behind.

---

## Fingerprinting

### The problem it solves

A single `node_modules` directory can't be safely shared across two
workspaces if one workspace's `package.json` disagrees with the
other's. A worktree checked out at an older commit expects an older
dependency tree; sharing the newer one silently breaks its runtime.

Fingerprinting picks a per-workspace variant of the shared resource
based on the files that determine its contents.

### How it works

A resource declares which files determine its contents:

```yaml
resources:
  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
      - "{root}/yarn.lock"
```

`wrk` hashes those inputs into a short digest and stores each variant
under its own subdirectory:

```
<data directory>/wrk/repositories/github.com/my-org/monolith/
    node_modules/
        5fd1d0d610ba6c17/   # package.json v1 + yarn.lock v1
        8a71d8b219fd0031/   # package.json v2 + yarn.lock v2
```

Each workspace's `node_modules` symlink points at the variant matching
its current fingerprint.

### Variants coexist

Two workspaces on different commits with different `package.json` files
get two independent variants. Switching branches inside a workspace
re-computes the fingerprint on the next `wrk link` and flips the
symlink to the matching variant. Reverting the change reuses the
earlier variant — the `initialize` hook does not run twice for the
same fingerprint.

Variants are accretive: `wrk` never deletes an older variant
automatically. This trades disk for speed and safety — jumping across
branches, or bisecting through commits, never triggers a reinstall for
a fingerprint you've already provisioned.

### Un-fingerprinted resources

Resources without a `fingerprint` section always use a single shared
copy:

```yaml
resources:
  - name: env
    path: .env
```

All workspaces see the same `.env`. This is the right choice for
files that are workspace-agnostic (secrets, editor state) but wrong
for anything whose correct contents depend on a manifest.

### How fingerprints are computed

The digest combines, for each input:

- Its repository-relative path.
- Its contents.
- Whether it exists.

Absolute filesystem paths are intentionally ignored, so two workspaces
of the same repository at the same commit always compute the same
fingerprint.

### Choosing fingerprint inputs

Fingerprint only the files that determine the resource's contents.

| Resource | Fingerprint |
|----------|-------------|
| `node_modules` | `package.json`, `yarn.lock`, `package-lock.json`, `pnpm-lock.yaml` |
| `vendor/bundle` | `Gemfile`, `Gemfile.lock`, `.ruby-version` |
| `.venv` | `pyproject.toml`, `poetry.lock`, `uv.lock`, `requirements.txt` |

Avoid fingerprinting files that change frequently but don't affect the
resource itself.

Every fingerprint input must resolve to a path inside the repository
root. `{root}/../secret` or `/etc/passwd` are rejected.

---

## Working across workspaces

`wrk` links a workspace resource by pointing its path at a shared
directory in `wrk`'s data directory. What that means for edits in a
workspace depends on whether the resource is fingerprinted.

### Editing files under a linked resource

A workspace's `node_modules/react/package.json` is a real file in
`wrk`'s shared storage, reached through the symlink. Editing it — or
running `patch-package`, adding a script to a dependency — mutates
shared storage directly. **Every workspace pointing at that variant
sees the change immediately.**

If you need isolation for a temporary patch, run `wrk detach` first.
That replaces the symlink with an independent copy so subsequent edits
stay local. `wrk relink --yes` discards the copy and reconnects.

### Installing a new dependency

Running `yarn add left-pad` in a workspace does two things:

1. Rewrites `package.json` and `yarn.lock` (real files in the
   worktree, tracked by git/jj).
2. Writes to `node_modules/` — which is the symlink into shared
   storage.

Step 2 mutates the shared variant. Every other workspace pinned to the
same fingerprint now has the new dependency without asking.

The next `wrk link` in this workspace notices the manifest changed and
moves the workspace's symlink to a **new** variant — leaving the
previous variant intact for other workspaces still on the old
`package.json`. The new variant is populated by re-running the
`initialize` hook.

If you want the change to belong to just this workspace during
development, `wrk detach` before installing.

### Switching commits

Checking out a different commit changes tracked files, including
manifests. If a fingerprint input changed, `wrk status` marks the
resource `stale` and `wrk link` flips the symlink to the matching
variant — provisioning it via the hook if the variant is new, reusing
it if not.

Un-fingerprinted resources don't move: switching commits doesn't
change their symlink target. `.env` stays the same across every
branch of the same repo.

---

## Typical workflow

Bootstrap a starter `.wrk.yml` for the repository (skip if you already have one):

```bash
wrk init
```

Initialize the primary workspace once:

```bash
wrk link
```

Create a new feature workspace as a sibling of the current one:

```bash
wrk new feature-auth
cd ../feature-auth
```

The new workspace is immediately ready to use.

Check state at any time:

```bash
wrk status
wrk workspaces
```

Need to experiment independently?

```bash
wrk detach
```

The workspace now has its own local copies of every managed resource.

Reconnect later (discarding the independent copies):

```bash
wrk relink
```

---

## Concurrency

`wrk` acquires a per-shared-resource lock (via `flock`) around any
provisioning action, so multiple workspaces racing to initialize the same
resource are serialized. The winner runs the initialize hook (or moves the
resource into shared storage); the loser skips the hook and links to the
winner's result.

---

## Philosophy

`wrk` intentionally has a small scope.

It does not:

- manage dependencies
- replace package managers
- version shared resources
- modify tracked project files
- modify repository history

Instead, it ensures every workspace contains the configured shared resources
and lets package managers do what they already do well.

---

## Status

`wrk` is experimental but intended for daily use.
