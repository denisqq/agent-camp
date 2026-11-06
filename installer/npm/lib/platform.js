// Platform mapping and release artifact naming shared by the installer and
// the launcher. Release archives use Go's arch names: Node's `x64` is
// `amd64`, `arm64` matches Go's `arm64`.
'use strict'

function releasePlatform(nodePlatform) {
  const map = { linux: 'linux', darwin: 'darwin', win32: 'windows' }
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
  const ext = platform === 'windows' ? '.zip' : '.tar.gz'
  return `camp-${platform}-${arch}${ext}`
}

function binaryName(nodePlatform) {
  return nodePlatform === 'win32' ? 'camp.exe' : 'camp'
}

function vendorDir(root, nodePlatform, nodeArch) {
  const platform = releasePlatform(nodePlatform)
  const arch = releaseArch(nodeArch)
  if (!platform || !arch) return null
  return require('path').join(root, 'vendor', `camp-${nodePlatform}-${nodeArch}`)
}

function releaseUrl(base, version, name) {
  return `${String(base).replace(/\/$/, '')}/v${version}/${name}`
}

module.exports = { releasePlatform, releaseArch, releaseName, binaryName, vendorDir, releaseUrl }
