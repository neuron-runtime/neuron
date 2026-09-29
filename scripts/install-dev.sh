#!/usr/bin/env bash
#
# install-dev.sh — make the current Neuron checkout feel installed.
#
# Builds the neuron CLI and the N.O.R.E. daemon from the current checkout into
# a repo-owned, gitignored directory:
#
#     .neuron/dev/
#     └── neuron         (application CLI binary)
#     └── nore           (N.O.R.E. daemon binary)
#     └── nore-daemon    (alias of the daemon binary)
#     └── echo-executor  (reference capability runtime, when built)
#
# then exposes them from ~/.local/bin via symlinks so the commands are usable
# from any directory without copying binaries into the system and without
# touching /usr/bin:
#
#     ~/.local/bin/
#     ├── neuron       -> <repo>/.neuron/dev/neuron
#     ├── nore         -> <repo>/.neuron/dev/nore
#     └── nore-daemon  -> <repo>/.neuron/dev/nore
#
# Symlinks, not copies: rebuilding the checkout (go build, then re-running this
# script) updates the installed commands in place)Skip stale copies.
#
# The installation is intentionally NON-destructive to user state: it never
# touches ~/.neuron/ (future capability runtime store / execution state). Use
# clean-dev.sh for a full reset of user-owned Neuron state.
#
# Prerequisites: Go 1.26.5+, git. ~/.local/bin must be on PATH (a hint is
# printed if it is missing).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_BIN_DIR="${NEURON_USER_BIN_DIR:-$HOME/.local/bin}"
DEV_DIR="$ROOT/.neuron/dev"

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------
for tool in go git; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: required tool not found: $tool" >&2
    echo "  neuron requires Go 1.26.5+ and git for development installs." >&2
    exit 1
  fi
done

if ! go version >/dev/null 2>&1; then
  echo "error: `go version` failed; is the Go toolchain installed correctly?" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Build binaries into the repo-owned dev directory
# ---------------------------------------------------------------------------
echo "==> Building neuron CLI"
mkdir -p "$DEV_DIR"
(
  cd "$ROOT" || exit 1
  go build -o "$DEV_DIR/neuron" ./application/cmd/neuron
)

echo "==> Building N.O.R.E. daemon"
(
  cd "$ROOT" || exit 1
  go build -o "$DEV_DIR/nore" ./nore/cmd/nore
)
# `nore-daemon` is an accepted alias for the daemon binary (the CLI also looks
# it up when locating the runtime). Both names point at the same dev binary.
ln -sfn "nore" "$DEV_DIR/nore-daemon"

# The reference echo capability runtime is optional; build it when it compiles
# so the first e2e smoke test has a real capability runtime to install.
if (cd "$ROOT/examples/capability-runtimes/echo" && GOWORK=off go build -o "$DEV_DIR/echo-executor" .) 2>/dev/null; then
  echo "==> Built reference echo capability runtime (examples/capability-runtimes/echo)"
else
  echo "==> Skipped reference echo capability runtime (does not compile; not required)"
fi

# ---------------------------------------------------------------------------
# Install symlinks into the user binary directory
# ---------------------------------------------------------------------------
echo "==> Installing development commands"
mkdir -p "$USER_BIN_DIR"

install_link() {
  local name="$1" target="$2"
  ln -sfn "$target" "$USER_BIN_DIR/$name"
  echo "  $USER_BIN_DIR/$name -> $target"
}

for name in neuron nore nore-daemon; do
  case "$name" in
    neuron)      install_link "$name" "$DEV_DIR/neuron" ;;
    nore)        install_link "$name" "$DEV_DIR/nore" ;;
    nore-daemon) install_link "$name" "$DEV_DIR/nore" ;;
  esac
done

# ---------------------------------------------------------------------------
# PATH hint
# ---------------------------------------------------------------------------
case ":$PATH:" in
  *":$USER_BIN_DIR:"*)
    echo
    echo "Development installation complete."
    echo
    echo "Verify from anywhere:"
    echo "  neuron version"
    echo "  nore version"
    ;;
  *)
    echo
    echo "Development installation complete, but $USER_BIN_DIR is not on PATH."
    echo "Add it (and re-source your shell), for example:"
    echo
    echo "  export PATH=\"$USER_BIN_DIR:\$PATH\""
    echo "  echo 'export PATH=\"$USER_BIN_DIR:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
    echo
    echo "Then verify from anywhere:"
    echo "  neuron version"
    echo "  nore version"
    ;;
esac
