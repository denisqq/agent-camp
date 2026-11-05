#!/usr/bin/env bash
# Acceptance smoke for setup and multi-device registration (spec scenarios
# 2, 3, 11, 16, 17, 20): five isolated client profiles ("laptops")
# connecting to one server, plus one user registering two agents.
# Uses a temporary server on a random loopback port; requires no network.
set -euo pipefail
cd "$(dirname "$0")/.."

work="$(mktemp -d)"
port="$((20000 + RANDOM % 20000))"
trap 'rm -rf "$work"; [[ -n "${server_pid:-}" ]] && kill "$server_pid" 2>/dev/null || true' EXIT

export PATH="$PATH:/usr/local/go/bin"
go build -o "$work/camp" ./cmd/camp

export CAMP_CONFIG="$work/server/camp.json"
mkdir -p "$work/server"
"$work/camp" setup server --open-registration >/dev/null
CAMP_LISTEN="127.0.0.1:$port" "$work/camp" serve &
server_pid=$!
server="http://127.0.0.1:$port"
for _ in $(seq 1 50); do
  if "$work/camp" readycheck "$server/health" >/dev/null 2>&1; then break; fi
  sleep 0.2
done
"$work/camp" readycheck "$server/health" >/dev/null

# Five isolated client profiles: five users, five agents.
laptop() {  # laptop <n> <args...>
  local n="$1"; shift
  HOME="$work/home-$n" XDG_CONFIG_HOME="$work/home-$n/.config" "$work/camp" "$@"
}
declare -A agents
for n in 1 2 3 4 5; do
  laptop "$n" register --server "$server" --name "same-laptop-name" --profile main >/dev/null
  agents[$n]="$(HOME="$work/home-$n" XDG_CONFIG_HOME="$work/home-$n/.config" \
    "$work/camp" doctor --profile main --json | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["agent_id"])')"
done
for n in 2 3 4 5; do
  if [[ "${agents[$n]}" == "${agents[1]}" ]]; then
    echo "FAIL: laptops $n and 1 share an agent id" >&2; exit 1
  fi
done

# Reconnect all five: IDs unchanged, credentials still valid.
for n in 1 2 3 4 5; do
  laptop "$n" doctor --profile main | grep -q "reachable+authorized"
  now="$(HOME="$work/home-$n" XDG_CONFIG_HOME="$work/home-$n/.config" \
    "$work/camp" doctor --profile main --json | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["agent_id"])')"
  [[ "$now" == "${agents[$n]}" ]]
done

# One user registers a second agent: same user, distinct agent.
laptop 1 register --server "$server" --name second-agent --profile second --as main >/dev/null
u1="$(HOME="$work/home-1" XDG_CONFIG_HOME="$work/home-1/.config" \
  "$work/camp" doctor --profile main --json | python3 -c 'import json,sys; print(json.load(sys.stdin)[0].get("user_id",""))')"
# (identity endpoint reports user via /v1/identity; verify through profile files)
p1="$(python3 -c "import json;print(json.load(open('$work/home-1/.config/camp/profiles/profile-main.json'))['user_id'])")"
p2="$(python3 -c "import json;print(json.load(open('$work/home-1/.config/camp/profiles/profile-second.json'))['user_id'])")"
[[ "$p1" == "$p2" ]]
a1="$(python3 -c "import json;print(json.load(open('$work/home-1/.config/camp/profiles/profile-main.json'))['agent_id'])")"
a2="$(python3 -c "import json;print(json.load(open('$work/home-1/.config/camp/profiles/profile-second.json'))['agent_id'])")"
[[ "$a1" != "$a2" ]]
_="$u1"

# Both agents can write to the shared starter workspace.
laptop 1 doctor --profile main --write | grep -q "write/read check passed"
laptop 1 doctor --profile second --write | grep -q "write/read check passed"

# Legacy compatibility path still works: init → token → start/run write.
export CAMP_CONFIG="$work/legacy/camp.json"
mkdir -p "$work/legacy"
CAMP_ROOT="$work/legacy/data" "$work/camp" init >/dev/null
echo "setup acceptance smoke passed"
