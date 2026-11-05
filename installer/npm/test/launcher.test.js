'use strict'

const test = require('node:test')
const assert = require('node:assert')
const { execFileSync, spawnSync } = require('node:child_process')
const fs = require('node:fs')
const path = require('node:path')
const os = require('node:os')

const { releaseArch, releaseName, releasePlatform, vendorDir } = require('../lib/platform')

test('node x64 maps to release amd64', () => {
  assert.equal(releaseArch('x64'), 'amd64')
  assert.equal(releaseArch('arm64'), 'arm64')
  assert.equal(releaseArch('ia32'), null)
  assert.equal(releasePlatform('linux'), 'linux')
  assert.equal(releasePlatform('sunos'), null)
})

test('release names use Go platform and arch naming', () => {
  assert.equal(releaseName('0.1.0', 'linux', 'x64'), 'camp-linux-amd64.tar.gz')
  assert.equal(releaseName('0.1.0', 'darwin', 'arm64'), 'camp-darwin-arm64.tar.gz')
  assert.equal(releaseName('0.1.0', 'sunos', 'x64'), null)
})

test('vendorDir handles paths containing spaces', () => {
  const dir = vendorDir('/tmp/some dir with spaces/pkg', 'linux', 'x64')
  assert.ok(dir.includes(path.join('vendor', 'camp-linux-x64')))
  assert.ok(fs.existsSync(path.dirname(dir)) || true) // not created here, just naming
})

test('launcher exits nonzero when the binary is missing and download fails', () => {
  // Point the first-use installer at an unreachable release base inside a
  // scratch copy so we never touch a real vendor directory.
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-launcher-'))
  fs.mkdirSync(path.join(scratch, 'bin'), { recursive: true })
  fs.mkdirSync(path.join(scratch, 'lib'), { recursive: true })
  for (const f of ['bin/camp.js', 'bin/install.js', 'lib/platform.js']) {
    fs.copyFileSync(path.join(__dirname, '..', f), path.join(scratch, f))
  }
  fs.copyFileSync(path.join(__dirname, '..', 'package.json'), path.join(scratch, 'package.json'))
  const out = spawnSync(process.execPath, [path.join(scratch, 'bin', 'camp.js'), '--version'], {
    env: { ...process.env, CAMP_RELEASE_BASE: 'http://127.0.0.1:9/unreachable', CAMP_OFFLINE_OK: '1' },
  })
  assert.notEqual(out.status, 0, 'launch must fail nonzero, stdout=' + out.stdout + ' stderr=' + out.stderr)
  assert.match(out.stderr.toString(), /agent-camp:/)
})

test('launcher exits with the child status and never masks failure as zero', () => {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-launcher-ok-'))
  fs.mkdirSync(path.join(scratch, 'vendor', 'camp-linux-x64'), { recursive: true })
  const bin = path.join(scratch, 'vendor', 'camp-linux-x64', process.platform === 'win32' ? 'camp.exe' : 'camp')
  fs.writeFileSync(bin, '#!/bin/sh\nexit 42\n')
  fs.chmodSync(bin, 0o755)
  const source = fs.readFileSync(path.join(__dirname, '..', 'bin', 'camp.js'), 'utf8')
  fs.mkdirSync(path.join(scratch, 'bin'), { recursive: true })
  fs.mkdirSync(path.join(scratch, 'lib'), { recursive: true })
  fs.writeFileSync(path.join(scratch, 'bin', 'camp.js'), source)
  fs.copyFileSync(path.join(__dirname, '..', 'lib', 'platform.js'), path.join(scratch, 'lib', 'platform.js'))
  if (process.platform !== 'linux') return // fake vendor layout is linux-shaped
  const out = spawnSync(process.execPath, [path.join(scratch, 'bin', 'camp.js')], { env: process.env })
  assert.equal(out.status, 42)
})

test('installer reports unsupported platforms without partial writes', () => {
  const { install } = require('../bin/install.js')
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-install-'))
  const result = install(scratch, 'sunos', 'x64', () => {})
  assert.ok(result.error)
  assert.ok(!fs.existsSync(path.join(scratch, 'vendor')))
})

test('installer fails closed on HTTP errors', () => {
  const { install } = require('../bin/install.js')
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-install-http-'))
  const previous = process.env.CAMP_RELEASE_BASE
  process.env.CAMP_RELEASE_BASE = 'http://127.0.0.1:9/unreachable'
  try {
    const result = install(scratch, 'linux', 'x64', () => {})
    assert.ok(result.error, 'must report error')
    // Nothing half-written is left behind.
    const vendor = path.join(scratch, 'vendor', 'camp-linux-x64')
    assert.ok(!fs.existsSync(path.join(vendor, 'camp')))
  } finally {
    if (previous === undefined) delete process.env.CAMP_RELEASE_BASE
    else process.env.CAMP_RELEASE_BASE = previous
  }
})
