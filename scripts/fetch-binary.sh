#!/bin/sh
# fetch-binary.sh — the plugin's [[build]] command: provision ./gotojira without
# requiring a Go toolchain.
#
# Downloads the prebuilt binary attached to the GitHub release matching the
# manifest's version (so an install pinned with --ref gets the binary that
# matches that checkout), and falls back to building from source when there is
# no usable asset (unsupported platform, missing release) or the download
# fails. Exits non-zero only when neither path works, which aborts the plugin
# install.
#
# Set GOTOJIRA_BUILD_FROM_SOURCE=1 to skip the download and always compile
# locally (for users who prefer not to run prebuilt binaries):
#   GOTOJIRA_BUILD_FROM_SOURCE=1 herdr plugin install asumaran/gotojira
set -eu

cd "$(dirname "$0")/.."

VERSION="$(sed -n 's/^version = "\(.*\)"/\1/p' herdr-plugin.toml)"
if [ -z "$VERSION" ]; then
  echo "fetch-binary: could not read version from herdr-plugin.toml" >&2
  exit 1
fi

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  *)      OS="" ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) ARCH=arm64 ;;
  x86_64)          ARCH=amd64 ;;
  *)               ARCH="" ;;
esac

URL="https://github.com/asumaran/gotojira/releases/download/v${VERSION}/gotojira-${OS}-${ARCH}"

if [ "${GOTOJIRA_BUILD_FROM_SOURCE:-0}" = "1" ]; then
  echo "fetch-binary: GOTOJIRA_BUILD_FROM_SOURCE=1, skipping release download"
elif [ -n "$OS" ] && [ -n "$ARCH" ] && command -v curl >/dev/null 2>&1; then
  tmp="$(mktemp)"
  if curl -fsSL --retry 2 -o "$tmp" "$URL"; then
    chmod +x "$tmp"
    mv "$tmp" gotojira
    echo "fetch-binary: installed gotojira-${OS}-${ARCH} from release v${VERSION}"
    exit 0
  fi
  rm -f "$tmp"
  echo "fetch-binary: no usable release asset at ${URL}; falling back to go build" >&2
fi

if command -v go >/dev/null 2>&1; then
  go build -ldflags "-X main.version=v${VERSION}-source" -o gotojira .
  echo "fetch-binary: built gotojira from source (v${VERSION}-source)"
  exit 0
fi

echo "fetch-binary: could not download a release binary and Go is not installed" >&2
exit 1
