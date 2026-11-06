#!/usr/bin/env node
// Thin launcher: runs the platform camp binary downloaded on install. If
// npm lifecycle scripts were skipped (or the download failed), it acquires
// the binary on first use. Launch failures exit nonzero.
'use strict'

const { spawnSync } = require('child_process')
const { existsSync } = require('fs')
const path = require('path')
const { binaryName, vendorDir } = require('../lib/platform')

const root = path.join(__dirname, '..')
const dir = vendorDir(root, process.platform, process.arch)
if (!dir) {
  console.error(`agent-camp: unsupported platform ${process.platform}-${process.arch}`)
  process.exit(1)
}
const bin = path.join(dir, binaryName(process.platform))
if (!existsSync(bin)) {
  const { install } = require('./install.js')
  const result = install(root, process.platform, process.arch, (m) => console.error('agent-camp: ' + m))
  if (result.error) {
    console.error('agent-camp: ' + result.error)
    process.exit(1)
  }
}
const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit', env: process.env })
if (res.error) {
  console.error('agent-camp: failed to launch ' + bin + ': ' + res.error.message)
  process.exit(1)
}
process.exit(res.status === null ? 1 : res.status)
