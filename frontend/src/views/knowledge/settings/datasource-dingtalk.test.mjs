import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const editor = readFileSync(resolve(here, 'DataSourceEditorDialog.vue'), 'utf8')
const icons = readFileSync(resolve(here, 'datasourceIcons.ts'), 'utf8')
const zh = readFileSync(resolve(here, '../../../i18n/locales/zh-CN.ts'), 'utf8')
const en = readFileSync(resolve(here, '../../../i18n/locales/en-US.ts'), 'utf8')
const ko = readFileSync(resolve(here, '../../../i18n/locales/ko-KR.ts'), 'utf8')
const ru = readFileSync(resolve(here, '../../../i18n/locales/ru-RU.ts'), 'utf8')
const dingtalkIconPath = resolve(here, '../../../assets/img/datasource-dingtalk.svg')

test('DingTalk is available in the data source editor', () => {
  assert.match(editor, /type:\s*'dingtalk'/)
  assert.match(editor, /key:\s*'app_key'/)
  assert.match(editor, /key:\s*'app_secret'/)
  assert.match(editor, /key:\s*'base_url'/)
})

test('DingTalk data source labels exist in Chinese and English locales', () => {
  assert.match(zh, /dingtalk:\s*"钉钉"/)
  assert.match(zh, /同步钉钉文档/)
  assert.match(en, /dingtalk:\s*'DingTalk'/)
  assert.match(en, /Sync DingTalk documents/)
})

test('DingTalk data source labels exist in all supported locales', () => {
  for (const locale of [zh, en, ko, ru]) {
    assert.match(locale, /dingtalk:/)
    assert.match(locale, /noResourcesDesc_dingtalk:/)
    assert.match(locale, /prereqBarText_dingtalk:/)
    assert.match(locale, /docSpace:/)
  }
})

test('DingTalk data source has a dedicated icon asset', () => {
  assert.match(icons, /datasource-dingtalk\.svg/)
  assert.match(icons, /dingtalk:\s*dingtalkIcon/)
  assert.equal(existsSync(dingtalkIconPath), true)
  assert.match(readFileSync(dingtalkIconPath, 'utf8'), /<svg[^>]+viewBox="0 0 1024 1024"/)
})
