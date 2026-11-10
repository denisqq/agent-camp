"""Trusted process adapter. Monty sees only explicit SDK methods, never this host."""
from __future__ import annotations

import argparse
import base64
import importlib.metadata
import json
import sys

from pydantic_monty import ClassInstance, CollectString, ExternalModule, Monty

MAX_FRAME = 2 * 1024 * 1024


def receive():
    line = sys.stdin.buffer.readline(MAX_FRAME + 1)
    if not line:
        raise EOFError
    if len(line) > MAX_FRAME or not line.endswith(b"\n"):
        raise ValueError("invalid frame")
    return json.loads(line)


def send(value):
    wire = json.dumps(value, ensure_ascii=False, allow_nan=False)
    if len(wire.encode()) > MAX_FRAME:
        raise ValueError("frame too large")
    sys.stdout.write(wire + "\n")
    sys.stdout.flush()


class Camp:
    def __init__(self):
        self.files = ClassInstance(Files(self), allowed_methods={"attach", "read_chunk"})

    def _call(self, method, args):
        send({"type": "call", "method": method, "args": args})
        reply = receive()
        if reply.get("error"):
            raise RuntimeError(reply["error"])
        return reply.get("result")

    def help(self, topic=""):
        return self._call("help", {"topic": topic})

    def describe(self, topic):
        return self.help(topic)

    def spaces(self):
        return self._call("spaces", {})

    def ls(self, path="/", cursor="", limit=50, space=""):
        return self._call("ls", dict(path=path, cursor=cursor, limit=limit, space=space))

    def map(self, path="/", depth=2, space="", *, view="adaptive", budget_bytes=12288):
        return self._call("map", dict(path=path, depth=depth, space=space, view=view, budget_bytes=budget_bytes))

    def find(self, path="/", kind="", metadata=None, cursor="", limit=50, space=""):
        return self._call("find", dict(path=path, kind=kind, metadata=metadata or {}, cursor=cursor, limit=limit, space=space))

    def read(self, path, start_line=1, limit=200, revision="", space=""):
        return self._call("read", dict(path=path, start_line=start_line, limit=limit, revision=revision, space=space))

    def stat(self, path, space=""):
        return self._call("stat", dict(path=path, space=space))

    def outline(self, path, space=""):
        return self._call("outline", dict(path=path, space=space))

    def write(self, path, content, kind="", metadata=None):
        args = dict(path=path, content=content, kind=kind)
        if metadata is not None:
            args["metadata"] = metadata
        return self._call("write", args)

    def annotate(self, path, metadata):
        return self._call("annotate", dict(path=path, metadata=metadata))

    def patch(self, path, old, new):
        return self._call("patch", dict(path=path, old=old, new=new))

    def move(self, path, to):
        return self._call("move", dict(path=path, to=to))

    def delete(self, path):
        return self._call("delete", dict(path=path))

    def history(self, path, limit=20, space=""):
        return self._call("history", dict(path=path, limit=limit, space=space))

    def diff(self, path):
        return self._call("diff", dict(path=path))

    def revert(self, path, revision):
        return self._call("revert", dict(path=path, revision=revision))

    def link(self, path, target, relation):
        return self._call("link", dict(path=path, target=target, relation=relation))

    def backlinks(self, path, space=""):
        return self._call("backlinks", dict(path=path, space=space))

    def comment(self, path, text, to=""):
        return self._call("comment", dict(path=path, text=text, to=to))

    def related(self, path):
        return self._call("related", dict(path=path))

    def propose(self, path):
        return self._call("propose", dict(path=path))

    def proposal_status(self, path):
        return self._call("proposal_status", dict(path=path))

    def search(self, query, limit=50, space="", cursor=""):
        return self._call("search", dict(query=query, limit=limit, space=space, cursor=cursor))

    def grep(self, query, limit=50, space="", cursor=""):
        return self._call("grep", dict(query=query, limit=limit, space=space, cursor=cursor))

    def refresh(self, space=""):
        return self._call("refresh", dict(space=space))


class Files:
    def __init__(self, camp):
        self._camp = camp

    def attach(self, path, handle):
        return self._camp._call("files.attach", dict(path=path, handle=handle))

    def read_chunk(self, path, offset=0, size=65536, space=""):
        result = self._camp._call("files.read_chunk", dict(path=path, offset=offset, size=size, space=space))
        return base64.b64decode(result["base64"], validate=True)


METHODS = {name for name in Camp.__dict__ if not name.startswith("_")}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--monty", required=True)
    args = parser.parse_args()
    for package in ("pydantic-monty", "pydantic-monty-client", "pydantic-monty-runtime"):
        if importlib.metadata.version(package) != "1.4.0":
            raise RuntimeError("Monty client/runtime version mismatch")
    with Monty(binary_path=args.monty, min_processes=1, max_processes=1, request_timeout=31) as pool:
        while True:
            try:
                request = receive()
            except EOFError:
                return
            try:
                code = request["code"]
                if not isinstance(code, str) or len(code.encode()) > 65536:
                    raise ValueError("code limit")
                output = CollectString(max_bytes=16384)
                module = ExternalModule({"camp": ClassInstance(Camp(), allowed_methods=METHODS, eager_attrs={"files"})})
                with pool.checkout(external_modules={"camp_sdk": module}, limits={"max_feed_duration_secs": 30, "max_memory": 64 * 1024 * 1024, "max_suspensions": 1000, "max_recursion_depth": 128}) as checkout:
                    value = checkout.feed_run(code, print_callback=output)
                result = {"type": "result", "result": value, "stdout": output.output}
                if len(json.dumps(result, ensure_ascii=False, allow_nan=False).encode()) > 16384:
                    raise ValueError("result limit")
                send(result)
            except Exception:
                # Program exceptions may embed private values. The coordinator tracks read dependencies.
                send({"type": "error", "error": "Python execution failed or exceeded its limits"})


if __name__ == "__main__":
    main()
