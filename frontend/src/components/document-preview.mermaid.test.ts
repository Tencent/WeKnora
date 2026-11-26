import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
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

test('the isolated frame renders every diagram in order and keeps invalid blocks separate', async () => {
  const bootstrap = readFileSync(new URL('./mermaid-file/render.js', import.meta.url), 'utf8')
  const parent = {}
  const doc = '<svg viewBox="0 0 20.2 30.1"></svg>'
  const rendered: Array<{ id: string; source: string }> = []
  let receive: (event: object) => Promise<void> = async () => assert.fail('missing listener')
  let response: unknown
  let closed = false
  const port = {
    postMessage(value: unknown) { response = structuredClone(value) },
    close() { closed = true },
  }
  runInNewContext(bootstrap, {
    window: { origin: 'null' },
    parent,
    URL,
    addEventListener(_name: string, fn: typeof receive) { receive = fn },
    removeEventListener() {},
    console: { error() {} },
    mermaid: {
      initialize() {},
      async render(id: string, source: string) {
        rendered.push({ id, source })
        if (source === 'invalid') throw new Error('invalid syntax')
        return { svg: doc }
      },
    },
    document: {
      baseURI: 'https://app.test/preview',
      createElement() {
        const attributes = new Map()
        return {
          content: {
            querySelector() {
              return {
                attributes,
                getAttribute() { return '0 0 20.2 30.1' },
                setAttribute(name: string, value: number) { attributes.set(name, value) },
              }
            },
          },
        }
      },
    },
    XMLSerializer: class {
      serializeToString(root: { attributes: Map<string, number> }) {
        return `<svg width="${root.attributes.get('width')}" height="${root.attributes.get('height')}"/>`
      }
    },
  })

  await receive({
    source: parent, origin: 'https://app.test', ports: [port],
    data: { config: {}, sources: ['first', 'invalid', 'last'] },
  })

  assert.deepEqual(rendered.map(item => item.source), ['first', 'invalid', 'last'])
  assert.equal(new Set(rendered.map(item => item.id)).size, 3)
  assert.deepEqual(response, ['<svg width="21" height="31"/>', null, '<svg width="21" height="31"/>'])
  assert.equal(closed, true)
})
