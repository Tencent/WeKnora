import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./DocumentListView.vue', import.meta.url), 'utf8')
const knowledgeBase = readFileSync(new URL('../KnowledgeBase.vue', import.meta.url), 'utf8')
const locales = [
  '../../../i18n/locales/zh-CN.ts',
  '../../../i18n/locales/en-US.ts',
  '../../../i18n/locales/ko-KR.ts',
  '../../../i18n/locales/ru-RU.ts',
].map(path => readFileSync(new URL(path, import.meta.url), 'utf8'))

test('list view renders journal ranks in a dedicated column', () => {
  assert.match(
    component,
    /class="cell cell-journal" role="columnheader"[^>]*>\s*{{ t\('knowledgeBase\.columnJournalRank'\) }}/,
  )
  assert.match(
    component,
    /<div class="cell cell-journal">[\s\S]*?<JournalRankBadges[\s\S]*?:rank="item\.metadata\?\.journal_rank"/,
  )
})

test('journal ranks have their own grid track between tags and source', () => {
  assert.match(
    component,
    /minmax\(80px, 0\.9fr\) \/\/ tag\s+minmax\(140px, 1\.25fr\) \/\/ journal rank\s+minmax\(86px, 0\.8fr\) \/\/ source/,
  )
})

test('all supported locales define the journal rank column heading', () => {
  for (const locale of locales) {
    assert.match(locale, /columnJournalRank:/)
  }
})

test('desktop toolbar keeps the list toggle clear of filters at common widths', () => {
  assert.doesNotMatch(knowledgeBase, /@media \(min-width: 1280px\)[\s\S]*?&__filters\s*{[\s\S]*?overflow-x: visible/)
  assert.match(knowledgeBase, /@media \(min-width: 1600px\)[\s\S]*?&__filters\s*{[\s\S]*?overflow-x: visible/)
})
