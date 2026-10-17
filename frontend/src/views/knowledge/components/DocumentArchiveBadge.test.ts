import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse } from '@vue/compiler-sfc'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { Tag } from 'tdesign-vue-next'

import enUS from '../../../i18n/locales/en-US.ts'
import jaJP from '../../../i18n/locales/ja-JP.ts'
import koKR from '../../../i18n/locales/ko-KR.ts'
import ruRU from '../../../i18n/locales/ru-RU.ts'
import zhCN from '../../../i18n/locales/zh-CN.ts'
import zhTW from '../../../i18n/locales/zh-TW.ts'

const LOCALES = {
  'en-US': enUS,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  'ko-KR': koKR,
  'ja-JP': jaJP,
  'ru-RU': ruRU,
} as const

const badgeSource = readFileSync(new URL('./DocumentArchiveBadge.vue', import.meta.url), 'utf8')
const cardView = readFileSync(new URL('./DocumentCardView.vue', import.meta.url), 'utf8')
const listView = readFileSync(new URL('./DocumentListView.vue', import.meta.url), 'utf8')
const { descriptor } = parse(badgeSource)

const valueAtPath = (bundle: unknown, path: string): string => {
  let node: unknown = bundle
  for (const part of path.split('.')) {
    if (node === null || typeof node !== 'object') return path
    node = (node as Record<string, unknown>)[part]
  }
  return typeof node === 'string' ? node : path
}

/** Render the badge SFC's real template with the real TDesign Tag. */
const renderBadge = async (locale: Record<string, unknown>): Promise<string> => {
  const app = createSSRApp({
    template: descriptor.template!.content,
    components: { TTag: Tag },
    setup() {
      return { t: (key: string) => valueAtPath(locale, key) }
    },
  })
  return renderToString(app)
}

test('a stored file renders a neutral archive badge in every locale', async () => {
  for (const [name, bundle] of Object.entries(LOCALES)) {
    const html = await renderBadge(bundle as Record<string, unknown>)
    const label = valueAtPath(bundle, 'knowledgeBase.statusStored')
    const hint = valueAtPath(bundle, 'knowledgeBase.storedHint')

    assert.ok(!label.startsWith('knowledgeBase.'), `${name}: statusStored is missing`)
    assert.ok(!hint.startsWith('knowledgeBase.'), `${name}: storedHint is missing`)
    assert.ok(html.includes(label), `${name}: badge HTML should contain "${label}": ${html}`)
    // The tooltip is what tells the user why the file never shows up in answers.
    assert.ok(html.includes(hint), `${name}: badge tooltip should contain "${hint}": ${html}`)
    // Neutral, never an error: the real TDesign Tag renders t-tag--<theme>.
    assert.match(html, /t-tag--default/, `${name}: badge should use the default/grey theme: ${html}`)
    for (const errorTheme of ['t-tag--danger', 't-tag--warning', 't-tag--success']) {
      assert.ok(!html.includes(errorTheme), `${name}: badge must not be ${errorTheme}: ${html}`)
    }
  }
})

test('the badge copy is the archive wording, not a failure or a draft', async () => {
  const zh = await renderBadge(zhCN as Record<string, unknown>)
  assert.ok(zh.includes('仅存档 · 未索引'), zh)
  const en = await renderBadge(enUS as Record<string, unknown>)
  assert.ok(en.includes('Archived · Not indexed'), en)
})

test('both document views render the badge for stored rows', () => {
  // Card grid: the branch sits in the parse-status chain, before the summary
  // fallback, and asks the shared badge component to render.
  assert.match(
    cardView,
    /<div v-else-if="item\.parse_status === 'stored'" class="card-analyze card-stored">\s*<DocumentArchiveBadge \/>/,
    'card view is missing the stored branch',
  )
  assert.match(cardView, /import DocumentArchiveBadge from '\.\/DocumentArchiveBadge\.vue'/)

  // List: the badge replaces the status tag for stored rows and the rest of the
  // status chain keeps rendering through statusByRow.
  assert.match(
    listView,
    /<DocumentArchiveBadge v-if="item\.parse_status === 'stored'" \/>\s*<template v-else-if="statusByRow\.get\(item\.id\) as StatusInfo \| undefined">/,
    'list view is missing the stored badge',
  )
  assert.match(listView, /import DocumentArchiveBadge from '\.\/DocumentArchiveBadge\.vue'/)
})

// The new branch is additive: the failure and success renderings that already
// existed must still be intact and still be the ones that own their statuses.
test('failed and completed renderings are unaffected', () => {
  assert.match(cardView, /v-else-if="item\.parse_status === 'failed'" class="card-analyze failure card-analyze-trace"/)
  assert.match(cardView, /\$t\('knowledgeBase\.parsingFailed'\)/)
  assert.match(
    cardView,
    /v-else-if="item\.parse_status === 'completed' && \(item\.summary_status === 'pending' \|\| item\.summary_status === 'processing'\)"/,
  )

  assert.match(
    listView,
    /if \(item\.parse_status === 'failed'\) \{\s*return \{ label: t\('knowledgeBase\.statusFailed'\), theme: 'danger', icon: 'close-circle' \}/,
  )
  assert.match(
    listView,
    /if \(item\.parse_status === 'completed'\) \{\s*return \{ label: t\('knowledgeBase\.statusCompleted'\), theme: 'success' \}/,
  )
  // `stored` must never fall through to the `--` placeholder or to completed.
  assert.doesNotMatch(
    listView,
    /if \(item\.parse_status === 'completed'\) \{\s*return \{ label: t\('knowledgeBase\.statusCompleted'\), theme: 'success' \};\s*\}\s*if \(item\.parse_status === 'stored'\)/,
  )
})
