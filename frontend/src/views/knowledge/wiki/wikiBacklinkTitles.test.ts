import assert from 'node:assert/strict'
import test from 'node:test'

import { resolveWikiBacklinkTitle } from './wikiBacklinkTitles.ts'

test('uses the response title for an unloaded backlink', () => {
  assert.equal(
    resolveWikiBacklinkTitle(
      'concept/gong-si-zi-ben-zhi-du',
      { 'concept/gong-si-zi-ben-zhi-du': '公司资本制度' },
      [],
    ),
    '公司资本制度',
  )
})

test('only uses own string titles and preserves fallback for prototype property names', () => {
  for (const slug of ['constructor', 'toString', '__proto__', 'hasOwnProperty']) {
    assert.equal(resolveWikiBacklinkTitle(slug, {}, []), slug)
    assert.equal(resolveWikiBacklinkTitle(slug, {}, [{ slug, title: 'Loaded title' }]), 'Loaded title')
    const titles = JSON.parse(JSON.stringify({ [slug]: '  中文标题  ' }))
    assert.equal(resolveWikiBacklinkTitle(slug, titles, []), '中文标题')
  }
  const inherited = Object.create({ 'concept/page': 'Inherited title' })
  assert.equal(resolveWikiBacklinkTitle('concept/page', inherited, []), 'page')
  for (const value of [undefined, null, 42, true, {}, [], () => 'title', '', '   ']) {
    const titles = { 'concept/page': value } as unknown as Record<string, string>
    assert.equal(resolveWikiBacklinkTitle('concept/page', titles, []), 'page')
    assert.equal(resolveWikiBacklinkTitle('concept/page', titles, [{ slug: 'concept/page', title: 'Loaded' }]), 'Loaded')
  }
})

test('prefers the response title and retains hierarchical slug fallback', () => {
  assert.equal(resolveWikiBacklinkTitle('concept/a', { 'concept/a': ' Current ' }, [{ slug: 'concept/a', title: 'Old' }]), 'Current')
  assert.equal(resolveWikiBacklinkTitle('concept/a/b', {}, []), 'a/b')
  assert.equal(resolveWikiBacklinkTitle('', {}, []), '')
})

test('keeps loaded page titles and slug fallback behavior', () => {
  assert.equal(
    resolveWikiBacklinkTitle('entity/acme', {}, [{ slug: 'entity/acme', title: 'Acme' }]),
    'Acme',
  )
  assert.equal(resolveWikiBacklinkTitle('concept/unknown-page', {}, []), 'unknown-page')
})
