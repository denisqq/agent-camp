#!/usr/bin/env node
// Thin launcher: runs the platform camp binary downloaded on install.
const { spawnSync } = require('child_process')
const path = require('path')
const os = require('os')
const bin = path.join(__dirname, '..', 'vendor', `camp-${os.platform()}-${os.arch()}`, process.platform === 'win32' ? 'camp.exe' : 'camp')
const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit', env: process.env })
process.exit(res.status || 0)
