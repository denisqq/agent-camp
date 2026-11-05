#!/usr/bin/env bash
# Build a versioned server runtime pack: the locked Python/Monty runner
# environment plus the adapter, packaged in the layout internal/runtimepack
# expects (runtime/bin/python, runtime/bin/monty,
# runtime/adapter/camp_runner.py, runtime/manifest.json).
#
# Usage: scripts/build-runtime.sh [output-dir]
set -euo pipefail

cd "$(dirname "$0")/.."
version="$(git describe --tags --always 2>/dev/null || echo dev)"
platform="linux-$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')"
out="${1:-dist}"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

echo "building runtime pack ${version} (${platform})"
uv sync --project runner --frozen
mkdir -p "${stage}/runtime/bin" "${stage}/runtime/adapter"
cp -L runner/.venv/bin/python "${stage}/runtime/bin/python"
cp -L runner/.venv/bin/monty "${stage}/runtime/bin/monty" 2>/dev/null || cp -L runner/.venv/bin/monty* "${stage}/runtime/bin/" 2>/dev/null || {
  echo "monty binary not found in runner/.venv; run scripts/build-runtime.sh after 'uv sync'" >&2
  exit 1
}
cp runner/camp_runner.py "${stage}/runtime/adapter/camp_runner.py"
cp -R runner/.venv/lib "${stage}/runtime/lib"
printf '{"version":"%s"}\n' "$version" >"${stage}/runtime/manifest.json"

mkdir -p "$out"
archive="${out}/camp-runtime-${version}-${platform}.tar.gz"
tar -czf "$archive" -C "$stage" runtime
sha256sum "$archive" >"${archive}.sha256"
echo "wrote ${archive}"
