import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./BatchMetadataDialog.vue', import.meta.url), 'utf8')
const zhCN = readFileSync(new URL('../../../i18n/locales/zh-CN.ts', import.meta.url), 'utf8')
const enUS = readFileSync(new URL('../../../i18n/locales/en-US.ts', import.meta.url), 'utf8')
const koKR = readFileSync(new URL('../../../i18n/locales/ko-KR.ts', import.meta.url), 'utf8')
const ruRU = readFileSync(new URL('../../../i18n/locales/ru-RU.ts', import.meta.url), 'utf8')

test('provides a replace-all metadata editor for selected documents', () => {
  assert.match(component, /dialog-class-name="batch-metadata-dialog"/)
  assert.match(component, /batchMetadataDialogHeading/)
  assert.match(component, /batchMetadataSubtitle/)
  assert.match(component, /batchMetadataHint/)
  assert.match(component, /maxMetadataFields = 20/)
  assert.match(component, /MetadataValueType = 'text' \| 'number' \| 'boolean' \| 'null'/)
  assert.match(component, /emit\('confirm', metadata\)/)
  assert.match(component, /metadataTypeOptions/)
  assert.match(component, /booleanOptions/)
})

test('defines batch metadata strings in every supported locale', () => {
  for (const locale of [zhCN, enUS, koKR, ruRU]) {
    assert.match(locale, /batchMetadata:/)
    assert.match(locale, /batchMetadataDialogHeading:/)
    assert.match(locale, /batchMetadataSubtitle:/)
    assert.match(locale, /batchMetadataHint:/)
    assert.match(locale, /batchMetadataSuccess:/)
    assert.match(locale, /batchMetadataFailed:/)
  }
})
