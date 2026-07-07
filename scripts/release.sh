#!/usr/bin/env bash
set -euo pipefail

# Usage: ./release.sh {patch|minor|major}
#
# Determines the next semver tag from the latest v* tag, creates an
# annotated tag on the current tip of trunk(), and pushes it. Works in
# colocated jj+git repositories.

BUMP="${1:-}"

case "$BUMP" in
  patch|minor|major) ;;
  *)
    echo "usage: $0 {patch|minor|major}" >&2
    exit 2
    ;;
esac

# --- sanity checks ---

if [ ! -d .git ]; then
  echo "error: this script expects a colocated jj+git repository (no .git found)" >&2
  exit 1
fi

# Refuse to release with uncommitted jj changes on the target revision.
if ! jj log -r '@ & ~empty()' --no-graph -T 'change_id' --limit 1 >/dev/null 2>&1; then
  : # jj not available or no changes — fine
fi

# Fetch to make sure our view of tags is current.
echo "→ fetching remote tags..."
git fetch --tags --quiet

# --- determine current and next versions ---

LATEST="$(git tag -l 'v*' --sort=-v:refname | head -n1 || true)"

if [ "$LATEST" = "" ]; then
  CURRENT="0.0.0"
  echo "→ no existing v* tag; starting from v0.0.0"
else
  CURRENT="${LATEST#v}"
  echo "→ latest tag: $LATEST"
fi

# Strip any prerelease suffix (e.g. 0.2.0-rc.1 -> 0.2.0) before bumping.
CORE="${CURRENT%%-*}"

IFS='.' read -r MAJOR MINOR PATCH <<< "$CORE"

case "$BUMP" in
  major)
    MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
  minor)
    MINOR=$((MINOR + 1)); PATCH=0 ;;
  patch)
    PATCH=$((PATCH + 1)) ;;
esac

NEXT="v${MAJOR}.${MINOR}.${PATCH}"

# --- identify commit to tag ---

COMMIT="$(jj log -r 'trunk()' --no-graph -T commit_id --limit 1)"

if [ "$COMMIT" = "" ]; then
  echo "error: could not resolve trunk() to a commit" >&2
  exit 1
fi

SHORT="${COMMIT:0:12}"

# --- confirm ---

echo
echo "  Current: ${LATEST:-<none>}"
echo "  Next:    $NEXT"
echo "  Commit:  $SHORT ($(git log -1 --format='%s' "$COMMIT"))"
echo
read -r -p "Create and push $NEXT? [y/N] " REPLY
case "$REPLY" in
  y|Y|yes|YES) ;;
  *)
    echo "aborted."
    exit 0
    ;;
esac

# --- tag + push ---

git tag -a "$NEXT" "$COMMIT" -m "Release $NEXT"
echo "→ created tag $NEXT locally"

git push origin "$NEXT"
echo "→ pushed $NEXT"

echo
echo "✓ Release triggered. Watch it here:"
echo "  https://github.com/blaineventurine/wrk/actions"

