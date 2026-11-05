# Common recipes; agents: run via `just <recipe>`
build:
	go build ./...

test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

check: build test vet

smoke:
	go build -o /tmp/camp ./camp-cli/cmd/camp
	rm -rf /tmp/campdata && CAMP_ROOT=/tmp/campdata /tmp/camp init
	CAMP_ROOT=/tmp/campdata CAMP_LISTEN=127.0.0.1:18090 /tmp/camp serve &
	sleep 1
	curl -sf http://127.0.0.1:18090/health
	kill %1

docker-build:
	docker build -t agent-camp:dev .

# Optional source-code navigation; keep the version in sync with MCP configs.
code-index:
	DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 build

code-index-check:
	DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 build --check

code-map:
	DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 map
