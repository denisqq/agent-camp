#!/usr/bin/env bash
# Container smoke: build the image, run server-only setup against a
# mounted configuration, start the foreground server, register an agent,
# and write/read a document. Then replace the container with the same
# persistent state and verify the saved credential still authenticates —
# control state (users, credentials, receipts) must survive replacement.
set -euo pipefail
cd "$(dirname "$0")/.."

image="agent-camp:smoke"
port="$((20000 + RANDOM % 20000))"
work="$(mktemp -d)"
trap 'rm -rf "$work"; docker rm -f camp-smoke >/dev/null 2>&1 || true' EXIT
mkdir -p "$work/config" "$work/data" "$work/home"
chmod -R 777 "$work"

echo "== build =="
docker build -qt "$image" .

mounts=(-v "$work/config:/config" -v "$work/data:/data" -v "$work/home:/home/camp" -e CAMP_CONFIG=/config/camp.json --user 10001:10001)

echo "== setup server =="
docker run --rm "${mounts[@]}" --entrypoint camp-server "$image" setup --open-registration | tail -1

echo "== serve =="
docker run -d --name camp-smoke -p "127.0.0.1:$port:8090" "${mounts[@]}" --entrypoint camp-server "$image" serve >/dev/null
for _ in $(seq 1 30); do
  if docker exec camp-smoke camp readycheck http://127.0.0.1:8090/ready >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec camp-smoke camp readycheck http://127.0.0.1:8090/ready

echo "== register, write/read =="
docker exec camp-smoke camp register --server http://127.0.0.1:8090 --name smoke-agent --profile smoke >/dev/null
docker exec camp-smoke camp doctor --profile smoke --write | grep -q "write/read check passed"

echo "== replace container, same persistent state =="
docker rm -f camp-smoke >/dev/null
docker run -d --name camp-smoke -p "127.0.0.1:$port:8090" "${mounts[@]}" --entrypoint camp-server "$image" serve >/dev/null
for _ in $(seq 1 30); do
  if docker exec camp-smoke camp readycheck http://127.0.0.1:8090/ready >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec camp-smoke camp doctor --profile smoke | grep -q "reachable+authorized"

echo "container smoke passed"
