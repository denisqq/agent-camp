// Package buildinfo carries the release version of this build. CI injects
// it via -ldflags; "dev" marks a development build, which changes setup
// behavior (no runtime-pack download, repository runner paths).
package buildinfo

// Version is the release version string ("v0.1.0") or "dev".
var Version = "dev"
