#!/usr/bin/env bash
# Package the built shared library into a release archive.
#   package.sh <version> <arch> <built-library> <dist-dir> <os>
set -euo pipefail

VERSION="${1:?version}"
ARCH="${2:?arch}"
LIBRARY="${3:?built library}"
DIST_DIR="${4:?dist dir}"
OS="${5:?os}"

VERSION="${VERSION#v}"

case "$OS" in
  linux) EXT="so" ;;
  darwin) EXT="dylib" ;;
  windows) EXT="dll" ;;
  *) echo "unsupported os: $OS" >&2; exit 1 ;;
esac

mkdir -p "$DIST_DIR"
DIST_DIR="$(cd "$DIST_DIR" && pwd)"
NAME="alysis_${VERSION}_${OS}_${ARCH}"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

cp "$LIBRARY" "$STAGE/alysis.$EXT"
cp README.md LICENSE "$STAGE/"

(cd "$STAGE" && zip -q -r "$DIST_DIR/$NAME.zip" .)

(cd "$DIST_DIR" && sha256sum "$NAME.zip" > checksums.txt)
echo "packaged $DIST_DIR/$NAME.zip"
