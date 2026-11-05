FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /camp ./cmd/camp

FROM python:3.12-slim-bookworm
RUN pip install --no-cache-dir uv==0.9.9
WORKDIR /opt/camp/runner
COPY runner/pyproject.toml runner/uv.lock runner/camp_runner.py ./
RUN uv sync --frozen --no-dev
COPY --from=build /camp /usr/local/bin/camp
RUN useradd --uid 10001 --create-home camp && mkdir -p /data && chown camp:camp /data
USER camp
ENV CAMP_ROOT=/data CAMP_LISTEN=0.0.0.0:8090 \
    CAMP_RUNNER_PYTHON=/opt/camp/runner/.venv/bin/python \
    CAMP_RUNNER_SCRIPT=/opt/camp/runner/camp_runner.py \
    CAMP_MONTY_BINARY=/opt/camp/runner/.venv/bin/monty
EXPOSE 8090
# Liveness only: /ready additionally checks storage and control state.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["camp", "readycheck", "http://127.0.0.1:8090/ready"]
ENTRYPOINT ["camp"]
