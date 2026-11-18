import assert from 'node:assert/strict'
import test from 'node:test'
import {
  resolveConfluenceAttachmentHref,
  rewriteConfluenceAttachmentAnchor,
} from './confluenceAttachmentLinks.ts'

const path = '/download/attachments/123/%E8%AF%B4%E6%98%8E.docx?version=1&api=v2#download'

test('joins the configured context at most once and preserves the attachment URL suffix', () => {
  const site = 'https://team.atlassian.net'
  assert.equal(resolveConfluenceAttachmentHref(path, site), site + path)
  assert.equal(resolveConfluenceAttachmentHref(path, site + '/wiki/'), site + '/wiki' + path)
  assert.equal(resolveConfluenceAttachmentHref('/wiki' + path, site + '/wiki'), site + '/wiki' + path)
  assert.equal(resolveConfluenceAttachmentHref('/wiki' + path, site), site + '/wiki' + path)
  assert.equal(resolveConfluenceAttachmentHref('/confluence' + path, site + '/confluence'), site + '/confluence' + path)
})

test('keeps attachment links unchanged without a usable site address', () => {
  for (const base of ['', 'invalid', 'file:///wiki', 'https://user:password@example.com']) {
    assert.equal(resolveConfluenceAttachmentHref(path, base), path)
  }
})

test('leaves other destinations alone', () => {
  for (const href of ['/pages/123', 'https://other.example' + path, '//other.example' + path]) {
    assert.equal(resolveConfluenceAttachmentHref(href, 'https://wiki.example'), href)
  }
})

test('only anchor hrefs are rewritten by the sanitization hook', () => {
  for (const nodeName of ['A', 'CODE', 'IMG']) {
    let href = path
    const node = {
      nodeName,
      getAttribute: () => href,
      setAttribute: (_name: string, value: string) => { href = value },
    } as unknown as Node
    rewriteConfluenceAttachmentAnchor(node, 'https://wiki.example')
    assert.equal(href, nodeName === 'A' ? 'https://wiki.example' + path : path)
  }
})
