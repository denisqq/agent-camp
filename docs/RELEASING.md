# Building and releasing Camp

## Validate a candidate without publishing

The `release` workflow runs on relevant pull requests and manual dispatches.
It builds the native client/server pair and standalone Python/Monty runtime on
Linux x86-64/ARM64 and macOS x86-64/ARM64, then installs the npm tarball against
local release fixtures. Acceptance includes registration, an SDK write/read,
MCP through the generated client launcher after npm-cache removal, and stable
identity on repeated setup. A separate job tests container replacement with
persistent data and credentials.

Local native candidate (requires Go, Node/npm, uv, Python 3, tar, and shasum):

```bash
version="v$(node -p "require('./installer/npm/package.json').version")"
mkdir -p dist
go build -trimpath -ldflags "-X github.com/denisqq/agent-camp/internal/buildinfo.Version=$version" -o dist/camp ./camp-cli/cmd/camp
go build -trimpath -ldflags "-X github.com/denisqq/agent-camp/internal/buildinfo.Version=$version" -o dist/camp-server ./camp-server/cmd/camp-server
# Example platform; use darwin/arm64/amd64 to match this machine.
tar -czf dist/camp-linux-amd64.tar.gz -C dist camp camp-server
shasum -a 256 dist/camp-linux-amd64.tar.gz > dist/camp-linux-amd64.tar.gz.sha256
scripts/build-runtime.sh "$version" dist
python3 scripts/smoke-release.py dist "$version"
```

The smoke uses temporary configuration, private client state, local HTTP release
fixtures and a disposable workspace. It does not publish anything or edit your
agent settings. A real runtime is downloaded only on a fresh local setup;
remote setup does not need Python/Monty on the client.

## Publisher access: one-time owner setup

A maintainer needs publishing rights to the npm name `agent-camp`. Confirm the
name and account before the first release; an unpublished package is not proof
that the name can be claimed. The workflow never logs into a human account.

For an existing package, configure npm Trusted Publishing for:

- GitHub owner: `denisqq`
- Repository: `agent-camp`
- Workflow filename: `release.yml`

The npm job has `id-token: write` and uses Node 24/npm with OIDC support. See
[npm Trusted Publishing](https://docs.npmjs.com/trusted-publishers/) for setup,
required versions and current publisher rules. For the first publication before
this trust exists, an owner can supply a short-lived, narrowly scoped credential
as GitHub Actions secret `NPM_TOKEN`, or perform the first publication manually
after authorizing it. Remove the bootstrap secret after configuring OIDC.

GitHub release uploads use the workflow's `GITHUB_TOKEN` with `contents: write`.
Container publication uses `packages: write` to push to
`ghcr.io/denisqq/agent-camp`. Ensure the repository can publish that package and
set its visibility to public so users can pull without logging in. No npm,
GitHub, or registry account is required to install public artifacts.

## Publish an approved version

Publishing requires explicit owner approval (see AGENTS.md). Do not create or
push a release tag as part of ordinary implementation work.

1. Merge the reviewed implementation and ensure native/container checks passed.
2. Set the npm package version. The tag must be exactly `v` plus that version.
3. After approval, create and push that tag.
4. The workflow first builds and verifies every platform. Only then does it
   upload GitHub release assets. npm and container publication follow that job.
5. Verify the installed public package and anonymous image pull. A successful
   candidate build does not establish registry permissions or publish success.

Prerelease tags publish npm's `next` channel; stable versions use `latest`.
The container has an exact version tag and stable releases also update `latest`.
The binary reports the injected tag with `camp --version` or
`camp-server --version`. No Windows artifacts are published yet.

## Installed files and recovery

The npm launcher installs both executables atomically into
`~/.camp/bin/<version>/` (`CAMP_INSTALL_DIR` overrides the parent). MCP client
settings contain an absolute path into that permanent directory. Previous
versions remain available for clients already configured to use them.
If one executable is missing from an installed version, rerunning the npm
launcher restores it from the verified package without removing the other one.

Client profiles and credentials use the OS configuration directory under Camp;
`CAMP_CLIENT_CONFIG_DIR` overrides that Camp directory for isolated installs.
Verified runtimes are installed under its `runtimes/` directory, outside
OS/npm caches. Tokens stay in private credential files, never in MCP settings.
Configuration stores resolved Python/adapter/Monty paths.
For an S3 deployment, `CAMP_STORE=s3`, `CAMP_S3_ENDPOINT`, and `CAMP_S3_BUCKET`
configure storage before setup validation, including on an empty config volume.

Setup state is per server URL, including scheme, host, port and path. Returning
to an endpoint reuses its saved identity after checking the credential. Local
administrator fallback applies only to local setup. Missing/revoked credentials
produce an actionable error; setup does not silently create another identity.
Use `camp register` to establish a replacement credential explicitly, then rerun
setup. External human identity providers are future work.
