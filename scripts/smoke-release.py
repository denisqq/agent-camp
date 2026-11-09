#!/usr/bin/env python3
"""Clean npm installation smoke against local, unpublished release artifacts.
Usage: python3 scripts/smoke-release.py ARTIFACT_DIRECTORY v0.1.0
Requires native camp/camp-server archive and runtime archive with checksums.
All configuration, credentials, npm cache and workspace data are temporary.
"""
import functools
import hashlib
import http.server
import json
import os
from pathlib import Path
import selectors
import shutil
import socket
import subprocess
import sys
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]


def run(args, cwd, env):
    result = subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, timeout=180)
    if result.returncode:
        raise RuntimeError(f"{args[0]} failed: {result.stderr}\n{result.stdout}")
    return result.stdout


def mcp_check(command, args, cwd, env):
    proc = subprocess.Popen([command, *args], cwd=cwd, env=env, text=True,
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    selector = selectors.DefaultSelector()
    selector.register(proc.stdout, selectors.EVENT_READ)

    def request(method, params, ident):
        proc.stdin.write(json.dumps(dict(jsonrpc="2.0", id=ident, method=method, params=params)) + "\n")
        proc.stdin.flush()
        while True:
            if not selector.select(60):
                raise RuntimeError("MCP response timeout")
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError("MCP bridge exited")
            response = json.loads(line)
            if response.get("id") == ident:
                if "error" in response:
                    raise RuntimeError(str(response["error"]))
                return response["result"]

    def tool(name, arguments, ident):
        result = request("tools/call", dict(name=name, arguments=arguments), ident)
        if result.get("isError"):
            raise RuntimeError(str(result))
        if result.get("structuredContent") is not None:
            return result["structuredContent"]
        return json.loads(next(c["text"] for c in result["content"] if c.get("type") == "text"))

    try:
        request("initialize", dict(protocolVersion="2025-03-26", capabilities={},
                                  clientInfo=dict(name="camp-release-smoke", version="1")), 1)
        proc.stdin.write(json.dumps(dict(jsonrpc="2.0", method="notifications/initialized")) + "\n")
        proc.stdin.flush()
        tools = request("tools/list", {}, 2)
        assert {t["name"] for t in tools["tools"]} == {"map", "start", "run"}
        tool("map", {}, 3)
        session = tool("start", {"primary_space": "workspace"}, 4)
        result = tool("run", dict(session_id=session["session_id"], operation_id="release-smoke",
                                  code="from camp_sdk import camp\ncamp.write('/smoke/release', 'installed through npm')\n{'saved': True}"), 5)
        assert result["status"] == "completed", result
    finally:
        selector.close()
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def main():
    artifacts = Path(sys.argv[1]).resolve()
    version = sys.argv[2]
    assert version.startswith("v"), "use the exact release tag (v0.1.0)"
    package = json.loads((ROOT / "installer/npm/package.json").read_text())
    assert version == "v" + package["version"], "npm and release versions differ"
    with tempfile.TemporaryDirectory(prefix="camp release smoke ") as tmp:
        work = Path(tmp)
        releases = work / "releases" / version
        releases.mkdir(parents=True)
        for archive in artifacts.glob("*.tar.gz"):
            expected = archive.with_name(archive.name + ".sha256").read_text().split()[0]
            assert hashlib.sha256(archive.read_bytes()).hexdigest() == expected
            shutil.copy2(archive, releases)
            shutil.copy2(archive.with_name(archive.name + ".sha256"), releases)
        handler = functools.partial(QuietHandler, directory=str(work / "releases"))
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        env = os.environ.copy()
        # No inherited checkout/runtime/credential settings can make this pass.
        for key in list(env):
            if key.startswith("CAMP_"):
                del env[key]
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{server.server_port}"
        env.update(CAMP_RELEASE_BASE=base, CAMP_RUNTIME_BASE=base,
                   CAMP_CONFIG=str(work / "config/camp.json"),
                   CAMP_CLIENT_CONFIG_DIR=str(work / "private"),
                   XDG_CACHE_HOME=str(work / "cache"),
                   CAMP_INSTALL_DIR=str(work / "install"),
                   CAMP_LISTEN=f"127.0.0.1:{port}",
                   npm_config_cache=str(work / "npm-cache"), npm_config_update_notifier="false")
        pkg = work / "package"
        shutil.copytree(ROOT / "installer/npm", pkg, ignore=shutil.ignore_patterns("vendor-*", "*.tgz", "node_modules"))
        shutil.copy2(ROOT / "README.md", pkg)
        shutil.copy2(ROOT / "LICENSE", pkg)
        packed = json.loads(run(["npm", "pack", "--json", "--ignore-scripts"], pkg, env))
        tarball = (packed[0] if isinstance(packed, list) else packed[package["name"]])["filename"]
        client = work / "client"
        client.mkdir()
        command = ["npx", "--yes", "--package", str(pkg / tarball), "camp"]
        launcher = None
        try:
            run([*command, "setup", "--clients", "cursor"], client, env)
            profile = json.loads((work / "private/profiles/profile-local.json").read_text())
            entry = json.loads((client / ".cursor/mcp.json").read_text())["mcpServers"]["agent-camp"]
            launcher = entry["command"]
            assert Path(launcher).is_absolute() and str(work / "install") in launcher
            assert version in run([launcher, "--version"], client, env)
            reports = json.loads(run([launcher, "doctor", "--write", "--json"], client, env))
            assert any("write/read check passed" in r.get("detail", "") for r in reports), reports
            shutil.rmtree(work / "npm-cache")
            mcp_check(launcher, entry["args"], client, env)
            run([launcher, "setup", "--clients", "cursor"], client, env)
            again = json.loads((work / "private/profiles/profile-local.json").read_text())
            assert again["agent_id"] == profile["agent_id"] and again["user_id"] == profile["user_id"]
            print("release smoke passed: npm install, setup, real SDK write, MCP after cache removal, stable reconnect")
        finally:
            if launcher:
                subprocess.run([launcher, "down"], cwd=client, env=env, capture_output=True, timeout=20)
            # Also stop a service started by setup before a later integration failed.
            for pidfile in (work / "config").rglob("server.pid"):
                try:
                    os.kill(int(pidfile.read_text()), 15)
                except (OSError, ValueError):
                    pass
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
