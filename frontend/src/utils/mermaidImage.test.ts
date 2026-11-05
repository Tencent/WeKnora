import assert from 'node:assert/strict'
import test from 'node:test'
import { DOMParser } from '@xmldom/xmldom'
import { mermaidImage } from './mermaidImage'

const parse = (svg: string) => new DOMParser().parseFromString(svg, 'image/svg+xml')

test('a Mermaid image keeps labels and styles encoded outside the host DOM', () => {
  const source =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="-8 2 420.5 160"><style>.node{fill:#f00}</style><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">设备归还 &amp; café</div></foreignObject></svg>'
  const wrapped = mermaidImage(source)
  assert(wrapped)
  const doc = parse(wrapped)
  const root = doc.documentElement
  assert(root)
  assert.equal(root.getAttribute('viewBox'), '-8 2 420.5 160')
  assert.equal(root.getAttribute('width'), '420.5')
  assert.equal(root.getAttribute('height'), '160')
  assert.equal(doc.getElementsByTagName('style').length, 0)
  assert.equal(doc.getElementsByTagName('foreignObject').length, 0)
  const image = doc.getElementsByTagName('image').item(0)!
  assert.equal(image.getAttribute('x'), '-8')
  assert.equal(image.getAttribute('y'), '2')
  const uri = image.getAttribute('href')!
  assert.match(uri, /^data:image\/svg\+xml;charset=utf-8,/)
  assert.equal(decodeURIComponent(uri.slice(uri.indexOf(',') + 1)), source)
})

test('untrusted SVG content cannot add active nodes or attributes to the wrapper', () => {
  const source = `<svg viewBox="0 0 100 50" onload="alert(1)"><script>alert(1)</script><style>body{background:url(https://private.example.test/a)}</style><a href="javascript:alert(1)"><text>link</text></a><image href="https://private.example.test/b"/></svg>`
  const wrapped = mermaidImage(source)!
  const doc = parse(wrapped)
  const root = doc.documentElement
  assert(root)
  assert.equal(root.childNodes.length, 1)
  assert.equal(doc.getElementsByTagName('script').length, 0)
  assert.equal(doc.getElementsByTagName('style').length, 0)
  assert.equal(doc.getElementsByTagName('a').length, 0)
  assert.equal(doc.getElementsByTagName('image').length, 1)
  assert.equal(root.hasAttribute('onload'), false)
  assert.equal(doc.getElementsByTagName('image').item(0)!.hasAttribute('onerror'), false)
})

test('invalid or missing geometry does not produce a preview wrapper', () => {
  for (const source of [
    '',
    '<svg/>',
    '<svg viewBox="0 0 Infinity 20"/>',
    '<svg viewBox="0 0 -1 20"/>',
    '<svg viewBox="0 0 20 0"/>',
    '<svg viewBox="0 0 20"/>',
    '<svg viewBox="0 0 1e999 20"/>',
    '<svg viewBox="0 0 20 20&quot; onload=&quot;alert(1)"/>',
  ])
    assert.equal(mermaidImage(source), null, source)
})

test('fractional, negative-offset and exponent geometry is normalized to numeric attributes', () => {
  const wrapped = mermaidImage('<svg viewBox="-1e1, +2, 2.5e2, 10.5"/>')!
  const root = parse(wrapped).documentElement
  assert(root)
  assert.equal(root.getAttribute('viewBox'), '-10 2 250 10.5')
  assert.equal(root.getAttribute('width'), '250')
  assert.equal(root.getAttribute('height'), '10.5')
})
