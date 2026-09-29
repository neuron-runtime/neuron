#!/usr/bin/env bash
#
# release.sh — build and package the Neuron 0.1.x release artifacts.
#
# Produces, for every supported platform, an archive containing the complete
# product (the `neuron` CLI and the N.O.R.E. daemon as its bundled runtime),
# plus a SHA256SUMS manifest covering every archive.
#
# Layout produced under the output directory (default ./dist):
#
#   dist/<version>/
#     neuron-<version>-<os>-<arch>.tar.gz   linux/darwin: contains neuron/neuron + neuron/nore
#     neuron-<version>-windows-amd64.zip    windows:      contains neuron/neuron.exe + neuron/nore.exe
#     SHA256SUMS
#
# Usage:
#   ./scripts/release.sh                      # version from the nearest git tag, else "dev"
#   ./scripts/release.sh --version v0.1.0     # explicit version
#   ./scripts/release.sh --out /tmp/dist      # output directory
#
# The version is the single authoritative release version: it is injected into
# both binaries through shared/version so `neuron version` and `nore version`
# report the same value.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null || echo dev)"
OUT_DIR="${REPO_ROOT}/dist"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="$2"
      shift 2
      ;;
    --out)
      OUT_DIR="$2"
      shift 2
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

LDFLAGS="-s -w -X github.com/neuron-runtime/neuron/shared/version.Version=${VERSION}"
TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

STAGING_DIR="$(mktemp -d)"
trap 'rm -rf "$STAGING_DIR"' EXIT

RELEASE_DIR="${OUT_DIR}/${VERSION}"
mkdir -p "$RELEASE_DIR"

echo ">> neuron release ${VERSION}"
echo ">> artifacts -> ${RELEASE_DIR}"
echo

for target in "${TARGETS[@]}"; do
  read -r os arch <<<"$target"

  bin_suffix=""
  if [[ "$os" == "windows" ]]; then
    bin_suffix=".exe"
  fi

  build_dir="${STAGING_DIR}/${os}-${arch}/neuron"
  mkdir -p "$build_dir"

  echo ">> building ${os}/${arch}"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build \
    -ldflags "$LDFLAGS" \
    -o "$build_dir/neuron${bin_suffix}" \
    "$REPO_ROOT/application/cmd/neuron"

  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build \
    -ldflags "$LDFLAGS" \
    -o "$build_dir/nore${bin_suffix}" \
    "$REPO_ROOT/nore/cmd/nore"

  (
    cd "$(dirname "$build_dir")"
    case "$os" in
      windows)
        archive_name="neuron-${VERSION}-windows-${arch}.zip"
        command -v zip >/dev/null 2>&1 \
          && zip -qr "$RELEASE_DIR/$archive_name" neuron \
          || tar -czf "$RELEASE_DIR/${archive_name%.zip}.tar.gz" neuron
        ;;
      linux|darwin)
        archive_name="neuron-${VERSION}-${os}-${arch}.tar.gz"
        tar -czf "$RELEASE_DIR/$archive_name" neuron
        ;;
    esac
  )
done

echo
echo ">> checksums"
(
  cd "$RELEASE_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum * > SHA256SUMS
  else
    shasum -a 256 * > SHA256SUMS
  fi
)
cat "$RELEASE_DIR/SHA256SUMS"

echo
echo ">> done. Release artifacts:"
ls -1 "$RELEASE_DIR"
echo
echo ">> Optional next step:"
echo "   gh release create ${VERSION}" \
     "$RELEASE_DIR"/neuron-${VERSION}-* "$RELEASE_DIR/SHA256SUMS" \
     --title "Neuron ${VERSION}" --notes ""