import assert from 'node:assert/strict'
import test from 'node:test'
import { spawnSync } from 'node:child_process'
import { buildCLIConnectCommand } from './cliIntegration'

test('CLI hosts preserve proxy prefixes and omit the SDK API suffix', () => {
  for (const [base, origin, host] of [
    ['https://kb.example.com/api/v1', 'https://ui.example.com', 'https://kb.example.com'],
    ['/app/weknora/api/v1', 'https://kb.example.com', 'https://kb.example.com/app/weknora'],
    ['http://127.0.0.1:19321/api/v1/', 'wails://wails.localhost', 'http://127.0.0.1:19321'],
    ['http://127.0.0.1:19321/api/v1', 'null', 'http://127.0.0.1:19321'],
  ]) {
    const command = buildCLIConnectCommand(base!, origin!)
    assert.ok(command.includes(`--host '${host}' --use &&\nweknora auth login`))
  }
})

test('unresolved desktop URLs use an explicit server placeholder', () => {
  assert.ok(buildCLIConnectCommand('/api/v1', 'null').includes("--host 'https://your-server.com'"))
  assert.ok(buildCLIConnectCommand('/api/v1', 'wails://wails.localhost').includes("--host 'https://your-server.com'"))
})

test('copyable host arguments remain literal in a POSIX shell', () => {
  const host = "https://kb.example.com/team'/$HOME/`printf-injected`/$(printf-injected)"
  const command = buildCLIConnectCommand(`${host}/api/v1`, 'https://kb.example.com')
  const argument = command.split(' --host ')[1]!.split(' --use')[0]!
  const result = spawnSync('/bin/sh', ['-c', `printf '%s' ${argument}`], { encoding: 'utf8' })
  assert.equal(result.status, 0)
  assert.equal(result.stderr, '')
  assert.equal(result.stdout, "https://kb.example.com/team'/$HOME/%60printf-injected%60/$(printf-injected)")
})
