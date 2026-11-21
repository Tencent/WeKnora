import assert from 'node:assert/strict'
import test from 'node:test'

import enUS from '../../i18n/locales/en-US.ts'
import jaJP from '../../i18n/locales/ja-JP.ts'
import koKR from '../../i18n/locales/ko-KR.ts'
import ruRU from '../../i18n/locales/ru-RU.ts'
import zhCN from '../../i18n/locales/zh-CN.ts'
import zhTW from '../../i18n/locales/zh-TW.ts'

const bundles = {
  'en-US': enUS,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  'ja-JP': jaJP,
  'ko-KR': koKR,
  'ru-RU': ruRU,
}

const keys = [
  'chat.feedback.like',
  'chat.feedback.dislike',
  'chat.feedback.cancel',
  'chat.feedback.reasonTitle',
  'chat.feedback.reasonPlaceholder',
  'chat.feedback.submit',
  'chat.feedback.reasonInaccurate',
  'chat.feedback.reasonIrrelevant',
  'chat.feedback.reasonOutdated',
  'chat.feedback.reasonIncomplete',
  'chat.feedback.reasonOther',
  'chat.feedback.failed',
  'knowledgeBase.feedback.likes',
  'knowledgeBase.feedback.dislikes',
  'knowledgeBase.feedback.approvalRate',
  'knowledgeBase.feedback.recallWeight',
  'knowledgeBase.feedback.needsOptimization',
  'knowledgeBase.feedback.filterAll',
  'knowledgeBase.feedback.filterLow',
  'knowledgeBase.feedback.filterNeedsOptimization',
  'knowledgeBase.feedback.detail',
  'knowledgeBase.feedback.sessions',
  'knowledgeBase.feedback.reasons',
  'knowledgeBase.feedback.weightLogs',
  'knowledgeBase.feedback.reset',
  'knowledgeBase.feedback.resetConfirm',
  'knowledgeBase.feedback.resetDone',
  'knowledgeBase.feedback.noFeedback',
  'knowledgeBase.feedback.emptyReasons',
  'knowledgeBase.feedback.emptyLogs',
  'knowledgeBase.feedback.emptySessions',
]

function read(bundle: Record<string, unknown>, path: string): unknown {
  return path.split('.').reduce<unknown>((current, part) => {
    if (!current || typeof current !== 'object') return undefined
    return (current as Record<string, unknown>)[part]
  }, bundle)
}

test('answer feedback copy exists in every locale', () => {
  for (const [locale, bundle] of Object.entries(bundles)) {
    for (const key of keys) {
      const value = read(bundle as Record<string, unknown>, key)
      assert.equal(typeof value, 'string', `${locale} ${key}`)
      assert.notEqual(String(value).trim(), '', `${locale} ${key}`)
    }
  }
})
