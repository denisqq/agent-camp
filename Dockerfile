FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CAMP_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/denisqq/agent-camp/internal/buildinfo.Version=$CAMP_VERSION" -o /camp ./camp-cli/cmd/camp && CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/denisqq/agent-camp/internal/buildinfo.Version=$CAMP_VERSION" -o /camp-server ./camp-server/cmd/camp-server

FROM python:3.12-slim-bookworm
RUN pip install --no-cache-dir uv==0.9.9
WORKDIR /opt/camp/runner
COPY runner/pyproject.toml runner/uv.lock runner/camp_runner.py ./
RUN uv sync --frozen --no-dev
COPY --from=build /camp /usr/local/bin/camp
COPY --from=build /camp-server /usr/local/bin/camp-server
RUN useradd --uid 10001 --create-home camp && mkdir -p /data /config && chown camp:camp /data /config
USER camp
ENV CAMP_ROOT=/data CAMP_LISTEN=0.0.0.0:8090 XDG_CONFIG_HOME=/config \
    CAMP_RUNNER_PYTHON=/opt/camp/runner/.venv/bin/python \
    CAMP_RUNNER_SCRIPT=/opt/camp/runner/camp_runner.py \
    CAMP_MONTY_BINARY=/opt/camp/runner/.venv/bin/monty
EXPOSE 8090
# Liveness only: /ready additionally checks storage and control state.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["camp", "readycheck", "http://127.0.0.1:8090/ready"]
ENTRYPOINT ["camp-server"]
CMD ["serve"]
