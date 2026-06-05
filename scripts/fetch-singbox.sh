#!/usr/bin/env bash
# Fetch a static sing-box release into internal/core/embedded/ for the
# single-file (embed_singbox) worker build. Official SagerNet release archives
# are statically linked, so the embedded binary runs on distroless/static and
# musl-based images alike.
#
# Usage: fetch-singbox.sh <os> <arch> [version]
#   os:      linux | darwin | windows
#   arch:    amd64 | arm64
#   version: sing-box version without the leading v (default: $SINGBOX_VERSION
#            or the pinned fallback below)
set -euo pipefail

OS="${1:?os required (linux|darwin|windows)}"
ARCH="${2:?arch required (amd64|arm64)}"
VERSION="${3:-${SINGBOX_VERSION:-1.13.13}}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dest_dir="${repo_root}/internal/core/embedded"
base="sing-box-${VERSION}-${OS}-${ARCH}"
url="https://github.com/SagerNet/sing-box/releases/download/v${VERSION}/${base}.tar.gz"

bin_name="sing-box"
[ "${OS}" = "windows" ] && bin_name="sing-box.exe"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

echo "fetching ${url}" >&2
curl -fsSL "${url}" -o "${tmp}/archive.tar.gz"
tar -xzf "${tmp}/archive.tar.gz" -C "${tmp}"

mkdir -p "${dest_dir}"
cp "${tmp}/${base}/${bin_name}" "${dest_dir}/sing-box"
chmod +x "${dest_dir}/sing-box"

# Record the checksum the embed verifies at extraction time.
( cd "${dest_dir}" && sha256sum sing-box | awk '{print $1}' > sing-box.sha256 )

echo "embedded ${OS}/${ARCH} sing-box ${VERSION} -> ${dest_dir}/sing-box" >&2
echo "sha256: $(cat "${dest_dir}/sing-box.sha256")" >&2
