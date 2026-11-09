'use strict'

const test = require('node:test')
const assert = require('node:assert')
const { spawnSync } = require('node:child_process')
const fs = require('node:fs')
const path = require('node:path')
const os = require('node:os')
const crypto = require('node:crypto')
const { execFileSync } = require('node:child_process')

const { releaseArch, releaseName, releasePlatform, vendorDir, installDir } = require('../lib/platform')

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
  assert.ok(dir.includes(path.join('vendor-linux-amd64')))
})

function scratchPackage() {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-launcher-'))
  for (const f of ['bin/camp.js', 'bin/install.js', 'lib/platform.js']) {
    fs.mkdirSync(path.dirname(path.join(scratch, f)), { recursive: true })
    fs.copyFileSync(path.join(__dirname, '..', f), path.join(scratch, f))
  }
  fs.copyFileSync(path.join(__dirname, '..', 'package.json'), path.join(scratch, 'package.json'))
  return scratch
}

function runCamp(scratch, args, env) {
  return spawnSync(process.execPath, [path.join(scratch, 'bin', 'camp.js'), ...args], {
    env: { ...process.env, ...env },
  })
}

test('launcher exits nonzero when the binary is missing and download fails', () => {
  const scratch = scratchPackage()
  const out = runCamp(scratch, ['--version'], { CAMP_RELEASE_BASE: 'http://127.0.0.1:9/unreachable', CAMP_INSTALL_DIR: path.join(scratch, 'bin-dir') })
  assert.notEqual(out.status, 0, 'launch must fail nonzero, stderr=' + out.stderr)
  assert.match(out.stderr.toString(), /agent-camp:/)
})

test('launcher promotes vendor binaries to the permanent install dir and propagates status', () => {
  const scratch = scratchPackage()
  const vendor = vendorDir(scratch, process.platform, process.arch)
  fs.mkdirSync(vendor, { recursive: true })
  for (const b of ['camp', 'camp-server']) {
    fs.writeFileSync(path.join(vendor, b), b === 'camp' ? '#!/bin/sh\nexit 42\n' : '#!/bin/sh\nexit 0\n')
    fs.chmodSync(path.join(vendor, b), 0o755)
  }
  const env = { CAMP_INSTALL_DIR: path.join(scratch, 'perm-bin') }
  const out = runCamp(scratch, [], env)
  assert.equal(out.status, 42, 'stderr=' + out.stderr)
  // The permanent directory holds BOTH binaries; MCP configs point here,
  // never into the npm cache.
  const version = require('../package.json').version
  assert.ok(fs.existsSync(path.join(scratch, 'perm-bin', version, 'camp')))
  assert.ok(fs.existsSync(path.join(scratch, 'perm-bin', version, 'camp-server')))
})

test('installer fails closed on unreachable release base', () => {
  const scratch = scratchPackage()
  const out = runCamp(scratch, [], { CAMP_RELEASE_BASE: 'http://127.0.0.1:9/unreachable', CAMP_INSTALL_DIR: path.join(scratch,'permanent') })
  assert.notEqual(out.status, 0)
  assert.ok(!fs.existsSync(path.join(vendorDir(scratch,process.platform,process.arch), 'camp')))

})

// The release fixture must run in a separate process: install() is
// synchronous (the launcher calls it before spawning the binary), and a
// same-process HTTP server would deadlock behind the blocked event loop.
// (stdout unused; the port is discovered below via the ready line)
const FIXTURE = `
const http = require('http')
const fs = require('fs')
const [archivePath, sumPath] = process.argv.slice(1)
const body = fs.readFileSync(archivePath)
const sum = fs.existsSync(sumPath) ? fs.readFileSync(sumPath, 'utf8') : null
const server = http.createServer((req, res) => {
  if (req.url.endsWith('.sha256')) {
    if (sum == null) { res.writeHead(404); res.end(); return }
    res.end(sum)
    return
  }
  res.end(body)
})
server.listen(0, '127.0.0.1', () => process.stdout.write('PORT=' + server.address().port + '\\n'))
`

function makePkgWithInstall() {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-pkg-'))
  fs.copyFileSync(path.join(__dirname, '..', 'package.json'), path.join(scratch, 'package.json'))
  for (const f of ['bin/install.js', 'lib/platform.js']) {
    fs.mkdirSync(path.dirname(path.join(scratch, f)), { recursive: true })
    fs.copyFileSync(path.join(__dirname, '..', f), path.join(scratch, f))
  }
  return scratch
}

function startFixture(archivePath, sumPath) {
  const { spawn } = require('node:child_process')
  const proc = spawn(process.execPath, ['-e', FIXTURE, archivePath, sumPath], { stdio: ['ignore', 'pipe', 'inherit'] })
  let port = ''
  proc.stdout.on('data', (d) => { port += d })
  const ready = new Promise((resolve) => {
    const iv = setInterval(() => {
      const m = /PORT=(\d+)/.exec(port)
      if (m) { clearInterval(iv); resolve(m[1]) }
    }, 20)
  })
  return { proc, port: ready, stop: () => proc.kill() }
}

test('installer verifies mandatory checksums and extracts both binaries', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'camp-install-verify-'))
  const stage = path.join(dir, 'stage')
  fs.mkdirSync(stage, { recursive: true })
  for (const b of ['camp', 'camp-server']) fs.writeFileSync(path.join(stage, b), '#!/bin/sh\necho ' + b + '\n')
  const archive = path.join(dir, 'camp.tgz')
  execFileSync('tar', ['-czf', archive, '-C', stage, 'camp', 'camp-server'])
  const body = fs.readFileSync(archive)
  const goodSum = crypto.createHash('sha256').update(body).digest('hex') + '  camp.tar.gz\n'
  const goodSumPath = path.join(dir, 'good.sha256')
  fs.writeFileSync(goodSumPath, goodSum)

  const runInstall = async (sumPath) => {
    const fixture = startFixture(archive, sumPath)
    const port = await fixture.port
    const scratch = makePkgWithInstall()
    const previous = process.env.CAMP_RELEASE_BASE
    process.env.CAMP_RELEASE_BASE = 'http://127.0.0.1:' + port
    try {
      // Fresh module path per scratch: BASE is captured at require time.
      const mod = require(path.join(scratch, 'bin', 'install.js'))
      const result = mod.install(scratch, 'linux', 'x64', () => {})
      return { scratch, result }
    } finally {
      if (previous === undefined) delete process.env.CAMP_RELEASE_BASE
      else process.env.CAMP_RELEASE_BASE = previous
      fixture.stop()
    }
  }

  // Matching checksum: success, both binaries extracted.
  const ok = await runInstall(goodSumPath)
  assert.ok(!ok.result.error, 'install failed: ' + ok.result.error)
  assert.ok(fs.existsSync(path.join(vendorDir(ok.scratch, 'linux', 'x64'), 'camp')))
  assert.ok(fs.existsSync(path.join(vendorDir(ok.scratch, 'linux', 'x64'), 'camp-server')))

  // Missing sidecar: refused, nothing extracted.
  const refused = await runInstall(null)
  assert.ok(refused.result.error && /unverified/.test(refused.result.error), 'want refusal, got ' + refused.result.error)

  // Corrupted checksum: refused.
  const badSumPath = path.join(dir, 'bad.sha256')
  fs.writeFileSync(badSumPath, 'deadbeef' + '0'.repeat(56) + '  camp.tar.gz\n')
  const bad = await runInstall(badSumPath)
  assert.ok(bad.result.error && /checksum mismatch/.test(bad.result.error), 'want mismatch refusal, got ' + bad.result.error)
})

test('launcher repairs an incomplete vendor pair before promotion', () => {
 const scratch=scratchPackage(), vendor=vendorDir(scratch,process.platform,process.arch)
 fs.mkdirSync(vendor,{recursive:true});fs.writeFileSync(path.join(vendor,'camp'),'#!/bin/sh\nexit 0\n');fs.chmodSync(path.join(vendor,'camp'),0o755)
 const out=runCamp(scratch,[],{CAMP_INSTALL_DIR:path.join(scratch,'permanent'),CAMP_RELEASE_BASE:'http://127.0.0.1:9/unreachable'})
 assert.notEqual(out.status,0);assert.match(out.stderr.toString(),/download failed/)
})

test('permanent binaries survive removal of the npm vendor cache', () => {
 const scratch=scratchPackage(), vendor=vendorDir(scratch,process.platform,process.arch), dir=path.join(scratch,'permanent')
 fs.mkdirSync(vendor,{recursive:true})
 for(const b of ['camp','camp-server']) {fs.writeFileSync(path.join(vendor,b),'#!/bin/sh\nexit 0\n');fs.chmodSync(path.join(vendor,b),0o755)}
 let out=runCamp(scratch,[],{CAMP_INSTALL_DIR:dir});assert.equal(out.status,0,out.stderr.toString())
 fs.rmSync(vendor,{recursive:true})
 out=runCamp(scratch,[],{CAMP_INSTALL_DIR:dir,CAMP_RELEASE_BASE:'http://127.0.0.1:9/unreachable'})
 assert.equal(out.status,0,out.stderr.toString())
})

for (const missing of ['camp', 'camp-server']) {
  test(`launcher repairs missing permanent ${missing}`, (t) => {
    const scratch = scratchPackage()
    t.after(() => fs.rmSync(scratch, { recursive: true, force: true }))
    const version = require('../package.json').version
    const vendor = vendorDir(scratch, process.platform, process.arch)
    const permanent = path.join(scratch, 'permanent')
    const installed = path.join(permanent, version)
    fs.mkdirSync(vendor, { recursive: true })
    fs.mkdirSync(installed, { recursive: true })
    for (const name of ['camp', 'camp-server']) {
      fs.writeFileSync(path.join(vendor, name), '#!/bin/sh\nexit 0\n', { mode: 0o755 })
      if (name !== missing) fs.copyFileSync(path.join(vendor, name), path.join(installed, name))
    }
    const result = runCamp(scratch, ['--version'], { CAMP_INSTALL_DIR: permanent })
    assert.equal(result.status, 0, result.stderr.toString())
    for (const name of ['camp', 'camp-server']) {
      assert.equal(fs.readFileSync(path.join(installed, name), 'utf8'), fs.readFileSync(path.join(vendor, name), 'utf8'))
    }
  })
}
