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
resources for your own machine. It is never committed — `wrk` automatically
ignores it via repository-local ignore rules.

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
| `path` | string | Workspace-relative path of the resource (file or directory). |
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

For example:

```yaml
resources:
  - name: env
    path: .env
```

`wrk` will never create `.env`. If it is missing everywhere, `wrk status`
reports it as `absent` and `wrk link` fails with a conflict — wrk has no
way to produce it and no reason to believe you have another source.

If the file *is* actually provided elsewhere (a secrets manager, `direnv`,
`1Password`, `sops`, a build-time template — anything outside wrk's scope),
tell wrk that missing is the correct state by setting `create: false`:

```yaml
resources:
  - name: env
    path: .env
    create: false
```

Now the state is `expected` instead of `absent`, `wrk link` skips it
silently, and `wrk status --exit-code` treats it as healthy. When the
external tool eventually drops the file into place, wrk will happily
share it across workspaces on the next `wrk link`.

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
(`package.json` + lockfile, `Gemfile`, `pyproject.toml` + `uv.lock`/`poetry.lock`,
`Pipfile.lock`, `requirements.txt`, `Cargo.toml`, `.env.example`) and writes a
`.wrk.yml` seeded with sensible defaults for each detected layout. It refuses
to overwrite an existing `.wrk.yml` unless you pass `--force`, and `--dry-run`
prints the generated file to stdout without touching disk.

```bash
wrk init --dry-run       # preview only
wrk init --force         # overwrite an existing .wrk.yml
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

> `wrk link` is intentionally conservative: it will never destroy a local
> copy. Use `wrk relink` when you explicitly want to throw away independent
> work and reconnect to shared storage. Destructive actions are flagged in
> the plan output with a ⚠ warning.

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
| `pending` | Nothing exists yet, but an `initialize` hook is configured. | `wrk link` — the hook runs, then the symlink is installed. |
| `missing` | The shared copy exists but the workspace symlink is not in place. | `wrk link`. |
| `not-linked` | A real local copy exists but no shared copy yet. | `wrk link` — the local copy moves into shared storage and a symlink takes its place. |
| `stale` | The workspace *is* a symlink, but it points at the wrong shared target. Almost always because a fingerprint input (e.g. `package.json`, `Gemfile.lock`) changed since the last `wrk link`, so a new shared variant now applies. | `wrk link` — the symlink is repointed to the current variant. |
| `conflict` | Both a real local copy AND a shared copy exist. `wrk link` refuses to clobber either. | Decide which one is authoritative. Delete the workspace copy and run `wrk link` to accept the shared copy, or run `wrk detach` to accept the workspace copy and keep it independent. |
| `absent` | Nothing exists anywhere, no `initialize` hook is configured, and `create` is `true`. wrk has no way to produce it. | Provide the file, add a hook, or (if the file is provided externally) set `create: false`. |

`wrk status --exit-code` treats every state except `linked`, `expected`, and `detached` as a problem — those three are stable resting states, everything else needs attention.

Workspace states are a rollup of the [resource states](#resource-states) above:

| State | Meaning |
|---|---|
| `linked` | Every resource is `linked` or `expected`. Nothing to do. |
| `detached` | Every resource is `detached`. |
| `partial` | A mix of `linked` and `detached` resources — deliberate mid-state. |
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

Unknown placeholders (typos like `{shred}` for `{shared}`) are rejected
at load time so a misspelled path never silently ships to disk.

---

## Fingerprinting

Some resources depend on the state of the repository.

For example, `node_modules` depends on:

```yaml
fingerprint:
  - "{root}/package.json"
  - "{root}/yarn.lock"
```

Whenever any fingerprint input changes, `wrk` computes a new fingerprint and
uses a different shared storage location.

For example:

```
package.json
yarn.lock

        │
        ▼

5fd1d0d610ba6c17

        │
        ▼

<data directory>/wrk/

    repositories/

        github.com/

            my-org/

                monolith/

                    node_modules/

                        5fd1d0d610ba6c17/
```

If the dependency manifests later change:

```
package.json
yarn.lock

        │
        ▼

8a71d8b219fd0031

        │
        ▼

<data directory>/wrk/

    repositories/

        github.com/

            my-org/

                monolith/

                    node_modules/

                        5fd1d0d610ba6c17/

                        8a71d8b219fd0031/
```

Both versions can exist simultaneously.

Creating a workspace on an older branch will automatically link to the
matching dependency installation if it already exists.

Resources without a `fingerprint` section always use a single shared copy.

For example:

```yaml
resources:
  - name: env
    path: .env
```

is always stored as:

```
<data directory>/wrk/

    repositories/

        github.com/

            my-org/

                monolith/

                    .env
```

### How fingerprints are computed

A fingerprint is based on:

- The repository-relative path of each fingerprint input.
- The contents of each fingerprint input.
- Whether each fingerprint input exists.

Absolute filesystem paths are intentionally ignored.

This means that different Jujutsu workspaces (or Git worktrees) for the same
repository always compute the same fingerprint when their dependency
manifests are identical.

### Choosing fingerprint inputs

Fingerprint only the files that determine the resource's contents.

Typical examples:

| Resource | Fingerprint |
|----------|-------------|
| `node_modules` | `package.json`, `yarn.lock`, `package-lock.json`, `pnpm-lock.yaml` |
| `vendor/bundle` | `Gemfile`, `Gemfile.lock`, `.ruby-version` |
| `.venv` | `pyproject.toml`, `poetry.lock`, `uv.lock`, `requirements.txt` |

Avoid fingerprinting files that change frequently but do not affect the
resource itself.

Every fingerprint input must resolve to a path inside the repository root.
`{root}/../secret` or `/etc/passwd` are rejected — the fingerprint's job
is to summarize what the repository declares about its own resource, not
arbitrary filesystem state.

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
