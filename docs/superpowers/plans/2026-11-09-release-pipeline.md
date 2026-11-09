# Finish the Camp installation and release pipeline

> **For agentic workers:** use superpowers:executing-plans. This continues the approved
> [onboarding design](../specs/2026-11-05-agent-camp-onboarding-design.md)
> and the user-authorized GLM handoff on `feat/release-pipeline`.

**Goal:** prepare working npm/native/container installation artifacts and a
reviewable release workflow without publishing a release.
**Tech stack:** existing Go, Node stdlib, locked Python/Monty, Docker and GitHub Actions.
**Architecture:** keep the inherited thin Node launcher, paired Go executables,
verified versioned Python/Monty runtime, server-bound private credentials, and
SQLite identities. No new runtime dependencies or changes to workspace format.

## Constraints and review focus

- Preserve and finish inherited GLM edits; retain previous working installations.
- Local admin credentials must never be sent to another server. Different ports
  are different endpoints. Repeated setup must validate existing credentials.
- Archive extraction must preserve safe relative links, validate the exact
  manifest version and required runtime files, and reject paths outside the pack.
- Downloaded binaries and runtime versions must agree with the npm version/tag.
- Test relocated runtime and npm installation outside the checkout/npm cache.
- Release/image/npm publication remains a separate user-authorized operation.
- No OIDC login implementation, Helm, HTTPS provisioning, or host infrastructure.

## Tasks

- [x] Runtime: reproduce broken archive links/manifest, fix safe extraction and
  activation, build a relocatable pack, execute it after moving/extracting.
- [x] Launcher: require both binaries, stage verified downloads, promote binaries
  atomically to versioned permanent directories; test cache removal and failures.
- [x] Setup: isolate identities per server, prevent remote admin fallback, verify
  saved credentials, retain workspace identity, persist absolute runner paths.
- [x] Delivery: inject release version, coordinate native pack/binary builds and
  clean-install smoke before npm/image publication; document publisher bootstrap.
- [x] Verify build/test/vet/race, npm tests, live setup/write/reconnect smoke
  and independent review.
- Delivery: commit, push and open PR; no tags/publish/merge.

## Execution record

- Handoff baseline: Go installer/config/client tests passed; inherited npm suite
  passed (7 cases), but included a network-dependent failure test.
- Decision: extend the existing installation approach rather than replace it;
  versioned permanent binaries preserve old MCP launchers across upgrades.

## Regression and acceptance evidence

- Observed failing regression tests before fixes: lost runtime interpreter links
  and manifest; incomplete/mismatched packs; local admin leakage to remote setup;
  different loopback endpoints sharing identities; npm vendor pair/cache removal;
  relative runner/config paths; explicit recovery ignored after credential
  revocation; cross-server profile leakage in `camp register`; archive writes
  through a symlink parent; duplicate control identities returning SQLite errors.
- Clean-install smoke initially reproduced the MCP bridge output-schema mismatch
  and repeated-bootstrap failure. Both were fixed, then the same packaged npm →
  setup → real SDK write → MCP after npm-cache removal → reconnect smoke passed
  on Linux amd64 using the built standalone runtime.
- Independent read-only review identified three Important issues (archive parent
  links, register endpoint isolation, explicit credential recovery). Each has a
  regression test observed failing before its fix and passing afterward.
- The reviewed minor about image version injection is addressed: the image job
  passes `CAMP_VERSION` to Docker.
- Container smoke passed: build, setup, register, SDK write/read, replace
  container with persistent state, reconnect with the same credential.

## Decisions and limits

- Retain the inherited architecture. Runtime symlinks stay within the pack and
  are validated before activation. Versioned binary directories preserve old
  launcher paths instead of overwriting executables used by GUI clients.
- Revoked credentials never trigger silent re-registration. An explicit valid
  registration profile for the same endpoint can replace rejected setup state.
- Candidate native Linux/macOS matrix is configured, but this environment only
  provides local Linux amd64 execution evidence. Registry permissions and npm
  Trusted Publishing must be configured by the owner before publication.
- No release tag, npm publication, image publication, or merge was performed.

Final local checks: full `go build ./...`, `go test -count=1 ./...`,
`go test -race -count=1 ./...`, `go vet ./...`, and all nine Node launcher
tests passed. Workflow/Compose YAML, shell syntax and `git diff --check` passed.

Additional acceptance: five isolated client profiles registered independent users
and agents, repeated doctor checks preserved IDs, and a second agent registered
under one established user; both agents completed real write/read checks.
The source smoke uses private temporary client directories on all platforms.
A regression test also covers installation under a symlinked cache directory
(the macOS `/var` → `/private/var` case).

`actionlint` passed for both CI/release workflows. CLI/server dependency
boundaries were checked with `go list -deps`; neither binary imports the other.

First PR CI: ordinary tests and Linux ARM64 native installation passed. The
container acceptance itself passed, but its EXIT cleanup failed on UID-10001
private files under an unprivileged GitHub runner. Cleanup now removes only
the smoke's mounted directories using a temporary root container before deleting
the runner-owned parent. This does not change product file permissions.

The macOS native smoke reached server startup but registration returned 400.
The installer used the unbounded machine hostname as its default display name,
while the registration API limits names to 64 bytes. Added a failing long-hostname
regression and bounded that default. Smoke retains the default name and reports
hostname byte length to validate that diagnosis on CI; its temporary root is
canonicalized for macOS `/private/var` paths.
