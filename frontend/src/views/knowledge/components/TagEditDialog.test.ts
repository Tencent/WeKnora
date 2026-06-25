import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const component = readFileSync(new URL('./TagEditDialog.vue', import.meta.url), 'utf8')
const zhCN = readFileSync(new URL('../../../i18n/locales/zh-CN.ts', import.meta.url), 'utf8')
const enUS = readFileSync(new URL('../../../i18n/locales/en-US.ts', import.meta.url), 'utf8')
const koKR = readFileSync(new URL('../../../i18n/locales/ko-KR.ts', import.meta.url), 'utf8')
const ruRU = readFileSync(new URL('../../../i18n/locales/ru-RU.ts', import.meta.url), 'utf8')

test('uses a compact flat dialog with selected and available sections', () => {
  assert.match(component, /dialog-class-name="tag-edit-dialog"/)
  assert.match(component, /width="400px"/)
  assert.match(component, /<template #header>/)
  assert.match(component, /class="tag-edit-heading-icon"/)
  assert.match(component, /name="discount"/)
  assert.match(component, /class="setting-drawer__section"/)
  assert.match(component, /class="setting-drawer__section-title"/)
  assert.match(component, /tagEditSelectedSection/)
  assert.match(component, /tagEditAvailableSection/)
  assert.match(component, /selectedTagsList/)
  assert.match(component, /availableTagsList/)
  assert.match(component, /class="tag-edit-chip"/)
  assert.match(component, /class="tag-edit-create-row"/)
  assert.match(component, /class="tag-edit-footer"/)
  assert.doesNotMatch(component, /class="tag-edit-create"/)
  assert.doesNotMatch(component, /class="tag-edit-count"/)
  assert.doesNotMatch(component, /:header="title"/)
  assert.doesNotMatch(component, /<t-checkbox/)
})

test('defines the short dialog heading in every supported locale', () => {
  for (const locale of [zhCN, enUS, koKR, ruRU]) {
    assert.match(locale, /tagEditDialogHeading:/)
    assert.match(locale, /tagEditSelectedSection:/)
    assert.match(locale, /tagEditAvailableSection:/)
  }
})
