#!/usr/bin/env bash
#
# clean-dev.sh — full development-environment reset.
#
# This is the *destructive* reset. It performs everything uninstall-dev.sh
# does, and then removes neuron-owned runtime/daemon state so the next
# `neuron run` starts from a clean slate:
#
#   • ~/.neuron/             local daemon data + socket + PID (real runtime state)
#   • <project>/.neuron/     any project build state underneath the checkout
#
# It intentionally never touches unrelated state in ~/.local/bin (only links it
# owns are removed) and never touches ~/.local/share/neuron.
#
# Because it deletes real daemon state, this script requires confirmation unless
# --yes is given. Use it when a frozen or wedged daemon needs a full reset.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_BIN_DIR="${NEURON_USER_BIN_DIR:-$HOME/.local/bin}"
DEV_DIR="$ROOT/.neuron/dev"

CONFIRM=""
for arg in "$@"; do
  case "$arg" in
    -y|--yes) CONFIRM=1 ;;
    *) echo "error: unknown argument: $arg" >&2; exit 1 ;;
  esac
done

if [ -z "$CONFIRM" ]; then
  if [ -t 0 ]; then
    read -r -p "Remove all neuron daemon state (~/.neuron, project .neuron, dev install)? [y/N] " answer
    case "$answer" in
      y|Y|yes|YES) ;;
      *) echo "aborted"; exit 1 ;;
    esac
  else
    echo "error: --yes required when running non-interactively" >&2
    exit 1
  fi
fi

# Reuse the owned-link and dev-dir removal logic from uninstall-dev.sh.
DEV_DIR="$DEV_DIR" ROOT="$ROOT" USER_BIN_DIR="$USER_BIN_DIR" \
  bash "$(dirname "${BASH_SOURCE[0]}")/uninstall-dev.sh"

echo
echo "==> Removing neuron-owned runtime state"

for dir in "$HOME/.neuron" "$HOME/.local/share/neuron" "$ROOT/.neuron"; do
  if [ -d "$dir" ]; then
    rm -rf "$dir"
    echo "  removed $dir"
  fi
done

rm -f "$HOME/.local/bin/nore-daemon" "$HOME/.local/bin/nore" "$HOME/.local/bin/neuron"

echo
echo "Full development reset complete."
