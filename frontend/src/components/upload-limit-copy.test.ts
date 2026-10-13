import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { createI18n } from 'vue-i18n'

import enUS from '../i18n/locales/en-US.ts'
import jaJP from '../i18n/locales/ja-JP.ts'
import koKR from '../i18n/locales/ko-KR.ts'
import ruRU from '../i18n/locales/ru-RU.ts'
import zhCN from '../i18n/locales/zh-CN.ts'
import zhTW from '../i18n/locales/zh-TW.ts'

const messages = {
  'en-US': enUS,
  'ja-JP': jaJP,
  'ko-KR': koKR,
  'ru-RU': ruRU,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
}

test('knowledge upload hints interpolate the configured limit in every locale', () => {
  const size = 73

  for (const locale of Object.keys(messages)) {
    const i18n = createI18n({ legacy: false, locale, messages })
    const pdfDoc = i18n.global.t('knowledgeBase.pdfDocFormat', { size })
    const textMarkdown = i18n.global.t('knowledgeBase.textMarkdownFormat', { size })

    assert.match(pdfDoc, /73/, `${locale}: pdf/doc hint`)
    assert.match(textMarkdown, /73/, `${locale}: text/markdown hint`)
  }
})

test('empty state and drag overlay pass the runtime limit to both upload hints', () => {
  for (const name of ['empty-knowledge.vue', 'upload-mask.vue']) {
    const source = readFileSync(new URL(`./${name}`, import.meta.url), 'utf8')

    assert.match(source, /import \{ MAX_FILE_SIZE_MB \} from '@\/utils'/)
    assert.match(
      source,
      /\$t\('knowledgeBase\.pdfDocFormat', \{ size: MAX_FILE_SIZE_MB \}\)/,
      `${name}: pdf/doc hint`,
    )
    assert.match(
      source,
      /\$t\('knowledgeBase\.textMarkdownFormat', \{ size: MAX_FILE_SIZE_MB \}\)/,
      `${name}: text/markdown hint`,
    )
  }
})
