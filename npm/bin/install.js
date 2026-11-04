#!/usr/bin/env node
// Downloads the camp binary for this platform from GitHub Releases.
const { createWriteStream, existsSync, mkdirSync } = require('fs')
const { chmodSync } = require('fs')
const https = require('https')
const os = require('os')
const path = require('path')
const { version } = require('../package.json')

const map = { linux: 'linux', darwin: 'darwin', win32: 'windows' }
const platform = map[os.platform()]
if (!platform) { console.error('agent-camp: unsupported platform ' + os.platform()); process.exit(0) }
const ext = platform === 'windows' ? '.zip' : '.tar.gz'
const name = `camp-${platform}-${os.arch()}${ext}`
const url = `https://github.com/denisqq/agent-camp/releases/download/v${version}/${name}`
const dir = path.join(__dirname, '..', 'vendor', `camp-${os.platform()}-${os.arch()}`)
if (existsSync(path.join(dir, platform === 'windows' ? 'camp.exe' : 'camp'))) process.exit(0)
mkdirSync(dir, { recursive: true })
console.error('agent-camp: downloading ' + url)
const file = createWriteStream(path.join(dir, name))
https.get(url, (res) => {
  if (res.statusCode >= 300 && res.headers.location) {
    https.get(res.headers.location, pipe); return
  }
  pipe(res)
})
function pipe(res) {
  res.pipe(file)
  file.on('finish', () => {
    file.close()
    if (ext === '.tar.gz') {
      const { execSync } = require('child_process')
      execSync(`tar -xzf ${path.join(dir, name)} -C ${dir}`)
      chmodSync(path.join(dir, 'camp'), 0o755)
    }
  })
}
