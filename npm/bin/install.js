#!/usr/bin/env node
// Downloads the camp binary for this platform from GitHub Releases.
// Failures exit nonzero, downloads land in a temporary file first and are
// verified against the published checksum before activation, and no shell
// string interpolation is used — paths with spaces are safe.
'use strict'

const { chmodSync, closeSync, existsSync, mkdirSync, openSync, renameSync, rmSync, writeFileSync } = require('fs')
const { execFileSync, spawnSync } = require('child_process')
const https = require('https')
const crypto = require('crypto')
const path = require('path')
const { version } = require('../package.json')
const { releaseName, binaryName, vendorDir, releaseUrl } = require('../lib/platform')

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
  const bin = path.join(dir, binaryName(nodePlatform))
  if (existsSync(bin)) return { ok: true, bin }
  mkdirSync(dir, { recursive: true })
  const url = releaseUrl(BASE, version, name)
  log('downloading ' + url)

  const body = fetch(url, 0)
  if (body == null) return { error: `download failed: ${url}` }
  const expected = checksumFor(name)
  if (expected && expected !== crypto.createHash('sha256').update(body).digest('hex')) {
    return { error: `checksum mismatch for ${name}; previous installation (if any) left untouched` }
  }

  const archive = path.join(dir, '.download' + path.extname(name))
  writeFileSync(archive, body)
  try {
    if (name.endsWith('.tar.gz')) {
      execFileSync('tar', ['-xzf', archive, '-C', dir], { stdio: 'ignore' })
    } else {
      execFileSync('unzip', ['-o', archive, '-d', dir], { stdio: 'ignore' })
    }
  } catch (err) {
    rmSync(archive, { force: true })
    return { error: 'extraction failed: ' + err.message }
  }
  rmSync(archive, { force: true })
  if (!existsSync(bin)) return { error: 'archive did not contain the camp binary' }
  chmodSync(bin, 0o755)
  return { ok: true, bin }
}

// fetch follows at most 5 redirects; returns null on any failure or
// non-200 status. Synchronous on purpose: the launcher calls install on
// first use before spawning the binary.
function fetch(url, redirects, redirHost) {
  if (redirects > 5) return null
  const out = spawnSync(process.execPath, ['-e', FETCH_WORKER, url], { maxBuffer: 256 << 20 })
  if (out.status !== 0) return null
  return Buffer.from(out.stdout, 'base64')
}

// Runs in a child process: performs the HTTPS GET, prints the body
// base64-encoded on stdout, exits nonzero on failure.
const FETCH_WORKER = `
const https = require('https')
const url = process.argv[2]
function get(u, redirects) {
  https.get(u, (res) => {
    if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location && redirects < 5) {
      res.resume()
      return get(new URL(res.headers.location, u).toString(), redirects + 1)
    }
    if (res.statusCode !== 200) { res.resume(); process.exit(1) }
    const chunks = []
    res.on('data', (c) => chunks.push(c))
    res.on('end', () => { process.stdout.write(Buffer.concat(chunks).toString('base64')) })
  }).on('error', () => process.exit(1))
}
get(url, 0)
`

function checksumFor(name) {
  // The release publishes <archive>.sha256 next to each archive; when it
  // is missing we do not fail the install (older releases), but newer
  // releases are verified.
  const sumUrl = releaseUrl(BASE, version, name + '.sha256')
  const body = fetch(sumUrl, 0)
  if (body == null) return null
  const match = /^([0-9a-f]{64})/i.exec(body.toString('utf8').trim())
  return match ? match[1].toLowerCase() : null
}

if (require.main === module) {
  const result = install(path.join(__dirname, '..'), process.platform, process.arch, (m) => console.error('agent-camp: ' + m))
  if (result.error) fail(result.error)
}

module.exports = { install, BASE }
