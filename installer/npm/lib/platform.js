// Platform mapping, release artifact naming, and install locations shared
// by the installer and the launcher. Release archives use Go's names:
// Node's `x64` is `amd64`, `arm64` matches Go's `arm64`.
'use strict'

const os = require('os')
const path = require('path')

function releasePlatform(nodePlatform) {
  const map = { linux: 'linux', darwin: 'darwin' }
  return map[nodePlatform] || null
}

function releaseArch(nodeArch) {
  const map = { x64: 'amd64', arm64: 'arm64' }
  return map[nodeArch] || null
}

function releaseName(version, nodePlatform, nodeArch) {
  const platform = releasePlatform(nodePlatform)
  const arch = releaseArch(nodeArch)
  if (!platform || !arch) return null
  return `camp-${platform}-${arch}.tar.gz`
}

function binaryNames() {
  return ['camp', 'camp-server']
}

function vendorDir(root, nodePlatform, nodeArch) {
  const platform = releasePlatform(nodePlatform)
  const arch = releaseArch(nodeArch)
  if (!platform || !arch) return null
  return path.join(root, `vendor-${platform}-${arch}`)
}

// The permanent install location. Client configs point here, NOT into the
// npm cache: `npm cache clean` or a package update must never break an
// installed MCP connection. CAMP_INSTALL_DIR overrides (tests, admins).
function installDir() {
  return process.env.CAMP_INSTALL_DIR || path.join(os.homedir(), '.camp', 'bin')
}

function releaseUrl(base, version, name) {
  return `${String(base).replace(/\/$/, '')}/v${version}/${name}`
}

module.exports = { releasePlatform, releaseArch, releaseName, binaryNames, vendorDir, installDir, releaseUrl }
