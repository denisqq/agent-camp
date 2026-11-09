#!/usr/bin/env bash
# Build a versioned, relocatable server runtime pack for one platform:
# a standalone CPython (uv-managed), a --relocatable venv with the locked
# runner dependencies, the adapter, and Monty. Output names match the
# downloader exactly: camp-runtime-<platform>.tar.gz(+.sha256), where
# platform is <goos>-<goarch> (linux/darwin, amd64/arm64).
#
# Must run on the target OS/arch: a runtime pack is not cross-buildable.
set -euo pipefail
cd "$(dirname "$0")/.."

version="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
case "$(uname -s)" in
  Linux)  goos=linux ;;
  Darwin) goos=darwin ;;
  *) echo "unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64)        goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac
platform="${goos}-${goarch}"

stage="$(mktemp -d)"
stage="$(cd "$stage" && pwd -P)"
out="${2:-dist}"
trap 'rm -rf "$stage"' EXIT

echo "building runtime pack ${version} (${platform})"
mkdir -p "${stage}/runtime/bin" "${stage}/runtime/adapter"

# Standalone CPython inside the pack: relocatable by construction, and the
# venv below references it with relative symlinks (--relocatable), so the
# whole tree can be moved after extraction.
uv python install 3.12.13 --install-dir "${stage}/runtime/py"
base_python="$(ls -d "${stage}"/runtime/py/cpython-3.12.*/bin/python3 2>/dev/null | head -1 || true)"
[[ -x "$base_python" ]] || { echo "standalone python not found after install" >&2; exit 1; }
uv venv "${stage}/runtime/venv" --python "$base_python" --relocatable
UV_PROJECT_ENVIRONMENT="${stage}/runtime/venv" uv sync --project runner --frozen --no-dev --link-mode copy

cp runner/camp_runner.py "${stage}/runtime/adapter/camp_runner.py"
for f in "${stage}/runtime/venv/bin/monty"; do
  [[ -f "$f" ]] || { echo "monty not present in the locked runner environment" >&2; exit 1; }
done

# Portable wrappers: resolve the pack root from $0, never bake in build
# paths, and never depend on a shell PATH.
cat >"${stage}/runtime/bin/python" <<WRAP
#!/bin/sh
ROOT=\$(CDPATH= cd -- "\$(dirname -- "\$0")/.." && pwd)
exec "\$ROOT/venv/bin/python3" "\$@"
WRAP
# monty ships as a native executable; wrap it for a stable path only.
cat >"${stage}/runtime/bin/monty" <<WRAP
#!/bin/sh
ROOT=\$(CDPATH= cd -- "\$(dirname -- "\$0")/.." && pwd)
exec "\$ROOT/venv/bin/monty" "\$@"
WRAP
chmod +x "${stage}/runtime/bin/python" "${stage}/runtime/bin/monty"

# uv's --relocatable fixes entry-point scripts but leaves the base
# interpreter symlink (and pyvenv.cfg home) absolute; re-point both at the
# copy of CPython inside the pack so the tree survives any move.
pydir="$(ls -d "${stage}"/runtime/py/cpython-3.12.* | head -1)"
pyname="$(basename "$pydir")"
ln -sfn "../../py/${pyname}/bin/python3" "${stage}/runtime/venv/bin/python"
sed -i.bak "s|^home = .*|home = ../py/${pyname}/bin|" "${stage}/runtime/venv/pyvenv.cfg"
rm -f "${stage}/runtime/venv/pyvenv.cfg.bak"

# uv also creates an absolute minor-version alias in its install directory.
# Rewrite every internal absolute link so no temporary build path survives.
"${stage}/runtime/bin/python" - "$stage" <<'PYLINK'
import os, pathlib, sys
root = pathlib.Path(sys.argv[1])
for link in root.rglob("*"):
    if link.is_symlink():
        target = os.readlink(link)
        if os.path.isabs(target):
            if os.path.commonpath([str(root), target]) != str(root):
                raise RuntimeError(f"external runtime link: {link}")
            link.unlink()
            link.symlink_to(os.path.relpath(target, link.parent))
PYLINK

printf '{"version":"%s"}\n' "$version" >"${stage}/runtime/manifest.json"

# Smoke the packaged runtime before shipping it: interpreter, Monty, and
# the adapter must all answer from inside the staged tree.
"${stage}/runtime/bin/python" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 12) else 1)'
"${stage}/runtime/bin/monty" --help >/dev/null
"${stage}/runtime/bin/python" "${stage}/runtime/adapter/camp_runner.py" --help >/dev/null

mkdir -p "$out"
archive="${out}/camp-runtime-${platform}.tar.gz"
tar -czf "$archive" -C "$stage" runtime
shasum -a 256 "$archive" >"${archive}.sha256"
echo "wrote ${archive}"
