#!/usr/bin/env node
// Keep each verified release outside npm's disposable cache. Existing MCP
// launchers retain their version while a new installation is activated.
'use strict'
const fs = require('fs')
const path = require('path')
const { spawnSync } = require('child_process')
const { version } = require('../package.json')
const { binaryNames, vendorDir, installDir } = require('../lib/platform')
const root = path.join(__dirname, '..')
const vendor = vendorDir(root, process.platform, process.arch)
if (!vendor) {
  console.error(`agent-camp: unsupported platform ${process.platform}-${process.arch}`)
  process.exit(1)
}
const binaries = binaryNames(process.platform)
const dir = path.join(installDir(), version)
const complete = (base) => binaries.every((b) => {
  try { return fs.statSync(path.join(base, b)).isFile() } catch { return false }
})
try {
  if (!complete(dir)) {
    if (!complete(vendor)) {
      const result = require('./install').install(root, process.platform, process.arch, (m) => console.error('agent-camp: ' + m))
      if (result.error) throw new Error(result.error)
    }
    fs.mkdirSync(installDir(), { recursive: true })
    const stage = fs.mkdtempSync(path.join(installDir(), '.install-'))
    try {
      for (const b of binaries) {
        fs.copyFileSync(path.join(vendor, b), path.join(stage, b))
        fs.chmodSync(path.join(stage, b), 0o755)
      }
      try {
        fs.renameSync(stage, dir)
      } catch (err) {
        if (!['EEXIST', 'ENOTEMPTY'].includes(err.code)) throw err
        // Repair missing files without removing an executable another process
        // may be using. Hard links publish complete staged files atomically;
        // concurrent repairers can safely find a file already installed.
        for (const b of binaries) {
          try { fs.linkSync(path.join(stage, b), path.join(dir, b)) } catch (linkErr) {
            if (linkErr.code !== 'EEXIST') throw linkErr
          }
        }
        if (!complete(dir)) throw err
      }
    } finally { fs.rmSync(stage, { recursive: true, force: true }) }
  }
  const result = spawnSync(path.join(dir, binaries[0]), process.argv.slice(2), { stdio: 'inherit', env: process.env })
  if (result.error) throw result.error
  process.exit(result.status === null ? 1 : result.status)
} catch (err) {
  console.error('agent-camp: ' + err.message)
  process.exit(1)
}
