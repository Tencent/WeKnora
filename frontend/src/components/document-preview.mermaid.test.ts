import assert from 'node:assert/strict'
import test from 'node:test'
import { createHash } from 'node:crypto'
import { DOMParser } from '@xmldom/xmldom'
import { frameDocument } from '../../scripts/mermaid-file-frame'
import { prepareDiagram } from './mermaid-file/prepareDiagram'

test('file previews keep HTML labels, styles and arrow references while removing navigation', () => {
  const doc = new DOMParser().parseFromString(
    '<svg xmlns="http://www.w3.org/2000/svg"><style>.node{fill:gold}</style>' +
      '<marker id="arrow"/><path marker-end="url(#arrow)"/><a href="https://example.com">' +
      '<foreignObject><div xmlns="http://www.w3.org/1999/xhtml">设备归还<br/>登记</div></foreignObject>' +
      '</a><script>evil()</script></svg>',
    'image/svg+xml',
  )
  prepareDiagram(doc.documentElement as unknown as SVGSVGElement)
  assert.equal(doc.getElementsByTagName('foreignObject')[0]?.textContent, '设备归还登记')
  assert.equal(doc.getElementsByTagName('br').length, 1)
  assert.equal(doc.getElementsByTagName('style')[0]?.textContent, '.node{fill:gold}')
  assert.equal(doc.getElementsByTagName('marker')[0]?.getAttribute('id'), 'arrow')
  assert.equal(doc.getElementsByTagName('path')[0]?.getAttribute('marker-end'), 'url(#arrow)')
  assert.equal(doc.getElementsByTagName('a').length, 0)
  assert.equal(doc.getElementsByTagName('script').length, 0)
})

test('the frame policy precedes and authorizes only the bundled renderer script', () => {
  const html = frameDocument('const example = "</script>";')
  const policy = html.match(/<meta http-equiv="Content-Security-Policy" content="([^"]+)">/)!
  const script = html.match(/<script>([\s\S]*)<\/script>/)!
  assert.ok(policy.index! < script.index!)
  assert.doesNotMatch(script[1]!, /<\/script/i)
  const directives = policy[1]!.split(';').map((rule) => rule.trim())
  const hash = createHash('sha256').update(script[1]!).digest('base64')
  assert.ok(directives.includes(`script-src 'sha256-${hash}'`))
  assert.ok(directives.includes("default-src 'none'"))
  assert.ok(directives.includes('img-src data:'))
  assert.ok(directives.includes("form-action 'none'"))
})
