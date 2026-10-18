import assert from 'node:assert/strict'
import test from 'node:test'
import { createI18n, type ComposerTranslation } from 'vue-i18n'

import { LOCALE_BUNDLES } from '../i18n/localeKeyAudit'
import { formatSyncError, formatSyncErrorSummary } from './syncError'

const i18n = createI18n({ legacy: false, locale: 'zh-CN', messages: LOCALE_BUNDLES })
const t = i18n.global.t as ComposerTranslation
// vue-i18n echoes a missing key back (and warns); the fallback path only needs
// that echo, so use a silent translator for unknown codes.
const missingKeyT = ((key: string) => key) as ComposerTranslation

test('a known sync error code renders in Chinese and keeps the document title', () => {
  assert.equal(
    formatSyncError({
      title: 'XPwkYGxZV3RX64wjuvlmXeo4WAgozOKL',
      code: 'dingtalk_document_failed',
      message: 'DingTalk multi-dimensional table could not be read; retry on the next sync',
    }, t),
    'XPwkYGxZV3RX64wjuvlmXeo4WAgozOKL — 无法读取钉钉文档，请检查访问权限后重试同步。',
  )
})

test('an unknown or missing code falls back to the backend message', () => {
  assert.equal(
    formatSyncError({ code: 'future_code', message: 'raw backend reason' }, missingKeyT),
    'raw backend reason',
  )
  assert.equal(formatSyncError({ code: 'future_code' }, missingKeyT), 'future_code')
  assert.equal(formatSyncError({ message: 'legacy reason' }, missingKeyT), 'legacy reason')
  assert.equal(formatSyncError({ title: 'doc', message: 'legacy reason' }, missingKeyT), 'doc — legacy reason')
  assert.equal(formatSyncError({}, missingKeyT), '')
})

test('the card uses the localised first sample and only falls back to error_message', () => {
  const raw = 'all fetched items failed during sync (1/1): DingTalk resource is unavailable'
  assert.equal(formatSyncErrorSummary(undefined, raw, t), raw)
  assert.equal(formatSyncErrorSummary([], raw, t), raw)

  const first = {
    title: 'XPwkYGxZV3RX64wjuvlmXeo4WAgozOKL',
    code: 'dingtalk_document_failed',
    message: 'English fallback',
  }
  const localised = 'XPwkYGxZV3RX64wjuvlmXeo4WAgozOKL — 无法读取钉钉文档，请检查访问权限后重试同步。'
  assert.equal(formatSyncErrorSummary([first], raw, t), localised)

  const more = formatSyncErrorSummary([
    first,
    { code: 'dingtalk_resource_failed', message: 'English fallback' },
    { code: 'ingest_failed', message: 'English fallback' },
  ], raw, t)
  assert.equal(more, `${localised} 等 2 条`)
  assert.doesNotMatch(more, /English fallback/)
})
