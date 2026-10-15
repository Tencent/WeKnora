import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MARKDOWN_CONTEXT_BYTES,
  MARKDOWN_PAGE_BYTES,
  findMarkdownPage,
  indexMarkdownBlob,
  readMarkdownPage,
} from './markdownPreviewPages.ts'

const bytes = (value: string) => new TextEncoder().encode(value).byteLength

test('small Markdown remains a single rendered page', async () => {
  const source = '# Heading\n\nA [linked paragraph][docs] with $x^2$.\n\n[docs]: https://example.com\n'
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)
  const page = await readMarkdownPage(blob, index, 0)

  assert.equal(index.pages.length, 1)
  assert.equal(page.plain, false)
  assert.equal(page.source, source)
})

test('large tables repeat their header without duplicating or dropping raw rows', async () => {
  const header = '| id | text |\n| --- | --- |\n'
  const rows = Array.from({ length: 18 }, (_, i) => `| row-${i} | ${'x'.repeat(40_000)} |\n`)
  const source = header + rows.join('')
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)

  assert.ok(index.pages.length > 1)
  const raw = await Promise.all(index.pages.map(page => blob.slice(page.start, page.end).text()))
  assert.equal(raw.join(''), source)
  for (let i = 1; i < index.pages.length; i++) {
    const page = await readMarkdownPage(blob, index, i)
    assert.equal(page.source.startsWith(header), true)
    assert.ok(bytes(page.source) <= MARKDOWN_PAGE_BYTES + MARKDOWN_CONTEXT_BYTES)
  }
})

test('an oversized Unicode line is split into bounded plain-text pages losslessly', async () => {
  const source = '前😀后'.repeat(MARKDOWN_PAGE_BYTES)
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)
  const raw = await Promise.all(index.pages.map(page => blob.slice(page.start, page.end).text()))

  assert.ok(index.pages.length > 2)
  assert.ok(index.pages.every(page => page.plain))
  assert.ok(index.pages.every(page => page.end - page.start <= MARKDOWN_PAGE_BYTES))
  assert.equal(raw.join(''), source)
  assert.equal(raw.some(part => part.includes('\uFFFD')), false)
})

test('a markup-dense table cannot render as one unbounded DOM page', async () => {
  const cells = Array.from({ length: 2_100 }, (_, i) => `c${i}`)
  const source = `| ${cells.join(' | ')} |\n| ${cells.map(() => '---').join(' | ')} |\n| ${cells.join(' | ')} |\n`
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)

  assert.ok(index.pages.length >= 3)
  assert.ok(index.pages.every(page => page.end - page.start <= MARKDOWN_PAGE_BYTES))
  const raw = await Promise.all(index.pages.map(page => blob.slice(page.start, page.end).text()))
  assert.equal(raw.join(''), source)
})

test('an oversized table row stays bounded and retains the table header context', async () => {
  const header = '| id | text |\n| --- | --- |\n'
  const huge = `| huge | ${'😀'.repeat(MARKDOWN_PAGE_BYTES)} |\n`
  const tail = '| tail | readable |\n'
  const blob = new Blob([header, huge, tail])
  const index = await indexMarkdownBlob(blob)
  const hugePages = index.pages.filter(page => page.start >= bytes(header) && page.end <= bytes(header + huge))

  assert.ok(hugePages.length > 1)
  for (const descriptor of hugePages) {
    const page = await readMarkdownPage(blob, index, index.pages.indexOf(descriptor))
    assert.equal(page.plain, true)
    assert.equal(page.source.startsWith(header), true)
  }
  const raw = await Promise.all(index.pages.map(page => blob.slice(page.start, page.end).text()))
  assert.equal(raw.join(''), header + huge + tail)
})

test('fenced code pages remain fenced and link definitions remain available', async () => {
  const opener = '```ts\n'
  const definition = '[docs]: https://example.com/docs\n'
  const source = `${definition}\n[guide][docs]\n\n${opener}${Array.from({ length: 40 }, () => `${'const x = 1; '.repeat(1_000)}\n`).join('')}\`\`\`\n`
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)

  assert.ok(index.pages.length > 1)
  const middle = await readMarkdownPage(blob, index, 1)
  assert.match(middle.source, /^```ts\n/)
  assert.match(middle.source, /```\n(?:\n\[docs\]:|$)/)
  assert.equal(index.definitions, definition)
})

test('citation page lookup ignores repeated synthetic table headers and rejects ambiguity', async () => {
  const header = '| id | text |\n| --- | --- |\n'
  const source = header + Array.from({ length: 12 }, (_, i) => `| unique-${i} | ${'x'.repeat(30_000)} |\n`).join('')
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)

  assert.equal(await findMarkdownPage(blob, index.pages, 'unique-9'), index.pages.findIndex(page => page.start <= source.indexOf('unique-9') && page.end > source.indexOf('unique-9')))
  assert.equal(await findMarkdownPage(blob, index.pages, '| id | text |'), 0)
  assert.equal(await findMarkdownPage(new Blob(['same\n', 'x'.repeat(MARKDOWN_PAGE_BYTES), '\nsame\n']), (await indexMarkdownBlob(new Blob(['same\n', 'x'.repeat(MARKDOWN_PAGE_BYTES), '\nsame\n']))).pages, 'same'), -1)
})


test('dense raw HTML uses plain text instead of mounting thousands of elements', async () => {
  const source = '<div>' + '<span>x</span>'.repeat(3000) + '</div>\n'
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)
  const page = await readMarkdownPage(blob, index, 0)
  assert.equal(page.plain, true)
  assert.equal(page.source, source)
})

test('oversized fence context cannot multiply the preview page budget', async () => {
  const fence = '`'.repeat(MARKDOWN_CONTEXT_BYTES)
  const source = fence + '\n' + ('code\n'.repeat(60000)) + fence + '\n'
  const blob = new Blob([source])
  const index = await indexMarkdownBlob(blob)
  assert.ok(index.pages.length > 1)
  for (let i = 0; i < index.pages.length; i++) {
    const page = await readMarkdownPage(blob, index, i)
    assert.ok(bytes(page.source) <= MARKDOWN_PAGE_BYTES + MARKDOWN_CONTEXT_BYTES)
    assert.equal(page.plain, true)
  }
  assert.equal((await Promise.all(index.pages.map(p => blob.slice(p.start, p.end).text()))).join(''), source)
})
