#!/usr/bin/env bash
#
# uninstall-dev.sh — remove the Neurdev dev installation and built binaries.
#
# This is the *non-destructive* reset:
#
#   • removes the neuron / nore symlinks it installed into ~/.local/bin,
#   • removes the built binaries + directories it created under .neuron/dev,
#   • does NOT touch ~/.neuron/ (real runtime daemon state is preserved) and
#     does NOT touch ~/.local/share/neuron or any project .neuron/ dirs.
#
# Only symlinks that this checkout actually owns are removed: each link is
# verified to resolve into this repository's .neuron/dev before it is deleted,
# so an unrelated `neuron`/`nore` on your PATH is never touched.
#
# See clean-dev.sh for the full reset.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_BIN_DIR="${NEURON_USER_BIN_DIR:-$HOME/.local/bin}"
DEV_DIR="$ROOT/.neuron/dev"

# Resolve a path to its canonical form, following symlinks.
resolve() {
  readlink -f "$1" 2>/dev/null || printf '%s' "$1"
}

# remove_owned_link deletes USER_BIN_DIR/<name> only when it is a symlink
# whose (resolved) target lives inside this checkout's dev dir.
remove_owned_link() {
  local name="$1"
  local link="$USER_BIN_DIR/$name"

  if [ ! -L "$link" ]; then
    return 0
  fi

  local target
  target="$(resolve "$link")"
  case "$target" in
    "$DEV_DIR/"*)
      rm "$link"
      echo "  removed $link -> $target"
      ;;
    *)
      echo "  skip $link (not owned by this checkout: $target)"
      ;;
  esac
}

echo "==> Removing dev commands"

remove_owned_link neuron
remove_owned_link nore
remove_owned_link nore-daemon

echo "==> Removing built dev binaries"

if [ -d "$DEV_DIR" ]; then
  rm -rf "$DEV_DIR"
  echo "  removed $DEV_DIR"
fi

echo
echo "Development uninstall complete."
echo "The following user state was intentionally left in place:"
echo
echo "  ~/.neuron/          (real daemon runtime state and data)"
echo "  ~/.local/share/neuron"
echo "  <project>/.neuron/  (project build state)"
echo
echo "Run ./scripts/clean-dev.sh for a full reset of neuron-owned state."
