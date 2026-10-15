#!/bin/sh
# Install hloop on Linux or macOS.
#   curl -fsSL https://raw.githubusercontent.com/eternitycircle/hloop/main/scripts/install.sh | sh
# Environment: HLOOP_REPO=eternitycircle/hloop (required if not baked in), HLOOP_VERSION=vX.Y.Z, HLOOP_BIN_DIR=/path
set -eu

REPO="${HLOOP_REPO:-eternitycircle/hloop}"
VERSION="${HLOOP_VERSION:-latest}"

case "$REPO" in OWNER/*) echo "Set HLOOP_REPO=<github-user>/<repo> (this script has not been customised yet)." >&2; exit 1;; esac

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in linux|darwin) ;; *) echo "Unsupported OS: $os (use scripts/install.ps1 on Windows)" >&2; exit 1;; esac
arch="$(uname -m)"
case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo "Unsupported CPU: $arch" >&2; exit 1;; esac

if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
  VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's|.*/tag/||')"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
ver="${VERSION#v}"
file="hloop_${ver}_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
echo "Downloading $file ..."
curl -fsSL "$base/$file" -o "$tmp/$file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want="$(grep " $file\$" "$tmp/checksums.txt" | awk '{print $1}')"
[ -n "$want" ] || { echo "No checksum listed for $file" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$file" | awk '{print $1}')"; else got="$(shasum -a 256 "$tmp/$file" | awk '{print $1}')"; fi
[ "$want" = "$got" ] || { echo "Checksum mismatch: refusing to install." >&2; exit 1; }

tar -xzf "$tmp/$file" -C "$tmp"
dest="${HLOOP_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
install -m 0755 "$tmp/hloop_${ver}_${os}_${arch}/hloop" "$dest/hloop"
echo "Installed $dest/hloop ($VERSION)"
case ":$PATH:" in *":$dest:"*) ;; *) echo "Add $dest to your PATH, e.g.:  echo 'export PATH=\"$dest:\$PATH\"' >> ~/.profile";; esac
echo "Next: cd into a project and run  hloop init   (new)  or  hloop adopt   (already in progress)"
