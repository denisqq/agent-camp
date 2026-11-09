#!/usr/bin/env node
// Downloads the release binaries (camp + camp-server) for this platform
// into the package vendor directory. The launcher (bin/camp.js) promotes
// them into the permanent install directory. Downloads land in a
// temporary file, are verified against the published checksum (mandatory),
// and both binaries must be present before the install succeeds. No shell
// string interpolation — paths with spaces are safe.
'use strict'

const { chmodSync, existsSync, mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } = require('fs')
const { execFileSync, spawnSync } = require('child_process')
const crypto = require('crypto')
const path = require('path')
const { version } = require('../package.json')
const { releaseName, vendorDir, releaseUrl, binaryNames } = require('../lib/platform')

const BASE = process.env.CAMP_RELEASE_BASE || 'https://github.com/denisqq/agent-camp/releases/download'

function fail(message) {
  console.error('agent-camp: ' + message)
  process.exit(1)
}

function install(root, nodePlatform, nodeArch, log) {
  const name = releaseName(version, nodePlatform, nodeArch)
  const dir = vendorDir(root, nodePlatform, nodeArch)
  if (!name || !dir) {
    return { error: `unsupported platform ${nodePlatform}-${nodeArch}` }
  }
  const binaries = binaryNames(nodePlatform)
  if (binaries.every((b) => existsSync(path.join(dir, b)))) return { ok: true, dir }
  mkdirSync(root, { recursive: true })
  const url = releaseUrl(BASE, version, name)
  log('downloading ' + url)

  const body = fetchSync(url)
  if (body == null) return { error: `download failed: ${url}` }
  const expected = checksumFor(name)
  if (expected == null) {
    return { error: `checksum for ${name} is unavailable; refusing unverified download` }
  }
  if (expected !== crypto.createHash('sha256').update(body).digest('hex')) {
    return { error: `checksum mismatch for ${name}; previous installation (if any) left untouched` }
  }

  const stage = mkdtempSync(path.join(root, '.download-'))
  const archive = path.join(stage, 'release.tar.gz')
  writeFileSync(archive, body)
  try {
    // Extract only the two executable payloads; unrelated archive paths or
    // symlinks cannot write outside this temporary installation directory.
    for (const b of binaries) {
      const data = execFileSync('tar', ['-xzOf', archive, b], { maxBuffer: 128 << 20 })
      if (!data.length) throw new Error('archive did not contain executable ' + b)
      writeFileSync(path.join(stage, b), data, { mode: 0o755 })
      chmodSync(path.join(stage, b), 0o755)
    }
    rmSync(archive)
    // An incomplete vendor directory is never used as a source by the launcher.
    rmSync(dir, { recursive: true, force: true })
    renameSync(stage, dir)
  } catch (err) {
    return { error: 'extraction failed: ' + err.message }
  } finally {
    rmSync(stage, { recursive: true, force: true })
  }
  return { ok: true, dir }
}

// fetchSync performs the HTTPS GET in a child process (the launcher calls
// install synchronously before spawning the binary) and follows at most 5
// redirects. Returns null on any failure or non-200 status.
function fetchSync(url) {
  const out = spawnSync(process.execPath, ['-e', FETCH_WORKER, url], { maxBuffer: 256 << 20, timeout: 180000 })
  if (out.status !== 0) return null
  // Buffer.from ignores the encoding for Buffer inputs: stringify first.
  return Buffer.from(out.stdout.toString('utf8'), 'base64')
}

const FETCH_WORKER = `
const url = process.argv[1]
function get(u, redirects) {
  const lib = u.startsWith('http:') ? require('http') : require('https')
  const req = lib.get(u, (res) => {
    if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location && redirects < 5) {
      res.resume()
      return get(new URL(res.headers.location, u).toString(), redirects + 1)
    }
    if (res.statusCode !== 200) { res.resume(); process.exit(1) }
    const chunks = []
    let size = 0
    res.on('data', (c) => { size += c.length; if (size > (128 << 20)) { req.destroy(); process.exit(1) }; chunks.push(c) })
    res.on('error', () => process.exit(1))
    res.on('end', () => { process.stdout.write(Buffer.concat(chunks).toString('base64')) })
  }).on('error', () => process.exit(1))
  req.setTimeout(30000, () => { req.destroy(); process.exit(1) })
}
get(url, 0)
`

// checksumFor fetches the published <archive>.sha256; a missing sidecar
// means an unverifiable download and fails the install.
function checksumFor(name) {
  const sumUrl = releaseUrl(BASE, version, name + '.sha256')
  const body = fetchSync(sumUrl)
  if (body == null) return null
  const match = /^([0-9a-f]{64})/i.exec(body.toString('utf8').trim())
  return match ? match[1].toLowerCase() : null
}

if (require.main === module) {
  const result = install(path.join(__dirname, '..'), process.platform, process.arch, (m) => console.error('agent-camp: ' + m))
  if (result.error) fail(result.error)
}

module.exports = { install, BASE }
