import assert from 'node:assert/strict'
import { test } from 'node:test'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { runInNewContext } from 'node:vm'

test('Docker entrypoint emits French runtime config and rejects non-whitelisted locale input', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'weknora-runtime-locale-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  for (const path of ['public', 'nginx/templates', 'nginx/conf.d', 'bin']) mkdirSync(join(dir, path), { recursive: true })
  const entrypoint = readFileSync(new URL('../../docker-entrypoint.sh', import.meta.url), 'utf8')
    .replaceAll('/usr/share/nginx/html', join(dir, 'public'))
    .replaceAll('/etc/nginx', join(dir, 'nginx'))
  writeFileSync(join(dir, 'entrypoint.sh'), entrypoint)
  writeFileSync(join(dir, 'nginx/templates/default.conf.template'), 'server {}')
  // Only nginx and envsubst are stubbed; execute the actual locale whitelist
  // and config generation in a disposable filesystem layout.
  writeFileSync(join(dir, 'bin/envsubst'), '#!/bin/sh\ncat\n', { mode: 0o755 })
  writeFileSync(join(dir, 'bin/nginx'), '#!/bin/sh\nexit 0\n', { mode: 0o755 })
  for (const [locale, expected] of [['fr-FR', 'fr-FR'], ['en-US', 'en-US'], ['', ''], ['es-ES', ''], ['fr-FR"; throw new Error("injected") //', '']]) {
    execFileSync('sh', [join(dir, 'entrypoint.sh')], {
      env: { PATH: `${join(dir, 'bin')}:${process.env.PATH}`, DEFAULT_LOCALE: locale },
    })
    const context = { window: {} as { __RUNTIME_CONFIG__?: { DEFAULT_LOCALE: string } } }
    runInNewContext(readFileSync(join(dir, 'public/config.js'), 'utf8'), context)
    assert.equal(context.window.__RUNTIME_CONFIG__?.DEFAULT_LOCALE, expected)
  }
})
