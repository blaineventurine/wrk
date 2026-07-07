# Installing wrk (beta testers)

`wrk` is currently in a private GitHub repository. This doc walks you
through installing it during the beta.

Two options — pick whichever is easier:

- **Option A: prebuilt binary via `gh` CLI** (recommended — no build tools required)
- **Option B: `go install` from source** (requires Go and a bit of setup)

---

## Prerequisites

- Access to `github.com/blaineventurine/wrk` (ask Blaine if you can't
  see the repo)
- For Option A: [`gh` CLI](https://cli.github.com/) authenticated with
  your GitHub account (`gh auth status` should show you as logged in)
- For Option B: Go 1.24+ (`go version`)

---

## Option A: prebuilt binary via `gh`

The release workflow publishes signed tarballs for macOS (arm64/x86_64)
and Linux (arm64/x86_64) on every tagged version.

### 1. Pick a version

```bash
gh release list --repo blaineventurine/wrk
```

### 2. Download and extract

```bash
# Substitute with your platform:
#   Darwin_arm64   — Apple Silicon macOS
#   Darwin_x86_64  — Intel macOS
#   Linux_arm64    — Linux ARM
#   Linux_x86_64   — Linux x86_64

VERSION=v0.1.2
PLATFORM=Darwin_arm64

gh release download "$VERSION" \
  --repo blaineventurine/wrk \
  --pattern "wrk_*_${PLATFORM}.tar.gz" \
  --output - | tar -xz -C /tmp

# Move it onto your PATH
sudo mv /tmp/wrk /usr/local/bin/
```

### 3. Verify

```bash
wrk --version
```

That's it. Skip to [First steps](#first-steps).

### Updating

Re-run the download step with a newer `VERSION`. See what's available
with `gh release list --repo blaineventurine/wrk`.

---

## Option B: `go install` from source

This builds the binary locally from the repo. It requires configuring
Go to work with private modules and git to authenticate to GitHub.

### 1. Configure Go to skip the module proxy for this repo

```bash
go env -w GOPRIVATE=github.com/blaineventurine/wrk
```

This tells Go not to route through `proxy.golang.org` (which can't see
private repos) and instead fetch directly via git.

You can verify:

```bash
go env GOPRIVATE
# should print: github.com/blaineventurine/wrk
```

### 2. Configure git to authenticate to GitHub

Choose whichever fits your existing setup:

#### If you already use SSH with GitHub

Add this once so Go's `https://` fetches use your SSH key:

```bash
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

Verify SSH works:

```bash
ssh -T git@github.com
# should print: Hi <you>! You've successfully authenticated...
```

#### If you use HTTPS with GitHub

Set up a credential helper and be ready to enter a
[Personal Access Token](https://github.com/settings/tokens) with
`repo` scope on first fetch:

```bash
# macOS
git config --global credential.helper osxkeychain

# Linux (systemd-based)
git config --global credential.helper cache
```

### 3. Install wrk

```bash
go install github.com/blaineventurine/wrk/cmd/wrk@latest
```

Or pin to a specific version:

```bash
go install github.com/blaineventurine/wrk/cmd/wrk@v0.1.2
```

The binary lands in `$(go env GOBIN)` (usually `~/go/bin`). Make sure
that's on your `PATH`.

### 4. Verify

```bash
wrk --version
```

### Updating

```bash
go install github.com/blaineventurine/wrk/cmd/wrk@latest
```

---

## First steps

Once installed, in any git or jj repository:

```bash
# Create a minimal .wrk.yml
cat > .wrk.yml <<'EOF'
resources:
  - name: env
    path: .env
    create: false
EOF

# See what wrk would do
wrk status

# When ready
wrk link
```

See the [README](../README.md) for a full walkthrough and the
[`examples/`](../examples/) directory for realistic configurations.

---

## Troubleshooting

### `go install ... : 404 Not Found`

The module proxy can't see the private repo. Confirm you configured
`GOPRIVATE`:

```bash
go env GOPRIVATE
```

Should include `github.com/blaineventurine/*` (or the specific repo).
If empty, re-run step 1 of Option B.

### `fatal: could not read Username for 'https://github.com'`

Git can't authenticate. Confirm you completed step 2 of Option B and
that either:

- `ssh -T git@github.com` succeeds (for the SSH path), or
- `git ls-remote https://github.com/blaineventurine/wrk` succeeds
  (for the HTTPS path)

### `gh: command not found`

Install `gh` from [cli.github.com](https://cli.github.com/), then
`gh auth login` and follow the prompts.

### `Permission denied` when moving to `/usr/local/bin`

Either use `sudo` (as shown), or install to a user-writable location:

```bash
mkdir -p ~/.local/bin
mv /tmp/wrk ~/.local/bin/
# Make sure ~/.local/bin is on your PATH
```

### The binary won't run on macOS ("cannot be opened because the developer cannot be verified")

macOS Gatekeeper. Remove the quarantine attribute:

```bash
xattr -d com.apple.quarantine $(which wrk)
```

---

## Uninstalling

```bash
# If installed via prebuilt binary:
sudo rm $(which wrk)

# If installed via go install:
rm "$(go env GOBIN)/wrk"
# or
rm ~/go/bin/wrk
```

To also remove wrk's shared storage on your machine:

```bash
rm -rf "${XDG_DATA_HOME:-$HOME/Library/Application Support}/wrk"
```

---

## Reporting issues

During the beta, please share feedback and bug reports with Blaine
directly, or open an issue in the private repository if you have
access.

Please include:

- Output of `wrk --version`
- The `wrk` command you ran
- The `.wrk.yml` (and `.wrk.local.yml` if any) — redact anything
  sensitive
- The output or error message
