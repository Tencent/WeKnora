import assert from 'node:assert/strict'
import test from 'node:test'
import { frameDocument } from './mermaid-file/frameDocument'

test('file renderer allows only its two local scripts and blocks external images', () => {
  const html = frameDocument('const text = "</script><img>"', 'start()')
  const nonce = html.match(/script-src 'nonce-([0-9a-f]{32})'/)?.[1]
  assert.ok(nonce)
  assert.ok(html.indexOf('Content-Security-Policy') < html.indexOf('<script'))
  assert.equal(html.split(`<script nonce="${nonce}">`).length - 1, 2)
  assert.equal(html.split('</script>').length - 1, 2)
  assert.match(html, /default-src 'none'/)
  assert.match(html, /img-src data:;/)
  assert.ok(!frameDocument('', '').includes(nonce))
})
