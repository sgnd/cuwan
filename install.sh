#!/bin/sh
# Install the latest cuwan release.
#
#   curl -fsSL https://raw.githubusercontent.com/sgnd/cuwan/main/install.sh | sh
#
# Installs to /usr/local/bin when writable, otherwise ~/.local/bin.
# Override with CUWAN_INSTALL_DIR. Pin a version with CUWAN_VERSION=v0.0.1.
set -eu

REPO="sgnd/cuwan"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  mingw* | msys* | cygwin*)
    echo "cuwan runs on macOS and Linux; Windows waits for a key store backend." >&2
    exit 1 ;;
  *) echo "cuwan supports macOS and Linux; there is no release for $os." >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "There is no cuwan release for this CPU ($(uname -m)): amd64 and arm64 only." >&2; exit 1 ;;
esac

for tool in curl tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "The installer needs $tool." >&2; exit 1; }
done

if [ -n "${CUWAN_INSTALL_DIR:-}" ]; then
  dir=$CUWAN_INSTALL_DIR
  mkdir -p "$dir"
elif [ -w /usr/local/bin ] 2>/dev/null || mkdir -p /usr/local/bin 2>/dev/null && [ -w /usr/local/bin ]; then
  dir=/usr/local/bin
else
  dir=${HOME}/.local/bin
  mkdir -p "$dir"
fi

ver=${CUWAN_VERSION:-latest}
if [ "$ver" = latest ]; then
  ver=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  if [ -z "$ver" ]; then
    echo "cuwan: no GitHub Release published yet. Build from source:" >&2
    echo "  git clone https://github.com/$REPO.git && cd cuwan && make build && ./bin/cuwan version" >&2
    exit 1
  fi
fi

asset="cuwan_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/${ver}/${asset}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/$asset"
curl -fsSL "https://github.com/$REPO/releases/download/${ver}/checksums.txt" -o "$tmp/checksums.txt"
want=$(awk -v a="$asset" '$2 == a { print $1 }' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "cuwan: the checksum of $asset does not match checksums.txt; nothing was installed." >&2
  exit 1
fi
tar -xzf "$tmp/$asset" -C "$tmp"
install -m 755 "$tmp/cuwan" "$dir/cuwan"
echo "Installed cuwan to $dir/cuwan"
"$dir/cuwan" version
