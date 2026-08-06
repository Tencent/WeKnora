import assert from 'node:assert/strict'
import test from 'node:test'

import { diffWikiRevision } from './wikiRevisionDiff.ts'

test('title-only edits produce a title section', () => {
  const sections = diffWikiRevision(
    { title: 'Old', summary: 'same', content: 'body' },
    { title: 'New', summary: 'same', content: 'body' },
  )
  assert.equal(sections.length, 1)
  assert.equal(sections[0].field, 'title')
  assert.ok(sections[0].lines.some((line) => line.type === 'del'))
  assert.ok(sections[0].lines.some((line) => line.type === 'add'))
})

test('summary-only edits produce a summary section', () => {
  const sections = diffWikiRevision(
    { title: 'same', summary: 'old summary', content: 'body' },
    { title: 'same', summary: 'new summary', content: 'body' },
  )
  assert.equal(sections.length, 1)
  assert.equal(sections[0].field, 'summary')
})

test('content edits still produce a content section', () => {
  const sections = diffWikiRevision(
    { title: 'same', summary: 'same', content: 'old body' },
    { title: 'same', summary: 'same', content: 'new body' },
  )
  assert.equal(sections.length, 1)
  assert.equal(sections[0].field, 'content')
})

test('mixed field edits preserve section order', () => {
  const sections = diffWikiRevision(
    { title: 'A', summary: 's1', content: 'c1' },
    { title: 'B', summary: 's2', content: 'c2' },
  )
  assert.deepEqual(
    sections.map((section) => section.field),
    ['title', 'summary', 'content'],
  )
})

test('first version diffs from empty baseline', () => {
  const sections = diffWikiRevision(
    { title: '', summary: '', content: '' },
    { title: 'Page', summary: 'Intro', content: 'Body' },
  )
  assert.deepEqual(
    sections.map((section) => section.field),
    ['title', 'summary', 'content'],
  )
  assert.ok(sections.every((section) => section.lines.every((line) => line.type === 'add')))
})
