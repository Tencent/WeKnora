import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const sourceDir = dirname(fileURLToPath(import.meta.url))
const pageSource = readFileSync(resolve(sourceDir, 'DingTalkDatasourceMockPage.vue'), 'utf8')
const serverSource = readFileSync(
  resolve(sourceDir, '../../../e2e/dingtalk-datasource-mock-server.mjs'),
  'utf8',
)

test('钉钉模拟演示固定使用中文界面与中文资源名称', () => {
  assert.match(pageSource, /locale\.value = 'zh-CN'/)
  assert.match(pageSource, /document\.title = '钉钉数据源接入演示 · WeKnora'/)
  assert.match(pageSource, /钉钉数据源接入流程/)
  assert.match(pageSource, /模拟演示环境/)
  assert.match(pageSource, /仅用于界面与请求链路验收/)
  assert.match(pageSource, /请求链路完成/)
  assert.match(pageSource, /配置保存成功/)
  assert.match(pageSource, /钉钉产品使用手册/)
  assert.match(pageSource, /钉钉产品协作空间/)

  assert.match(serverSource, /name: '钉钉产品协作空间'/)
  assert.match(serverSource, /name: '使用指南'/)
  assert.match(serverSource, /name: '钉钉产品使用手册'/)
})

test('钉钉模拟演示不再显示原有英文文案', () => {
  for (const legacyText of [
    'Development-only mock E2E',
    'Mock 演示环境',
    'DingTalk data-source flow',
    'Run again',
    'UI save receipt',
    'Mock Product Space',
    'Guides',
    'DingTalk mock handbook',
  ]) {
    assert.equal(pageSource.includes(legacyText), false, `页面仍包含英文文案：${legacyText}`)
    assert.equal(serverSource.includes(legacyText), false, `模拟服务仍包含英文文案：${legacyText}`)
  }
})

test('钉钉模拟演示具备完整视觉层级与移动端适配', () => {
  assert.match(pageSource, /mock-page__provider/)
  assert.match(pageSource, /mock-page__notice/)
  assert.match(pageSource, /mock-receipt__success/)
  assert.match(pageSource, /mock-receipt__path/)
  assert.match(pageSource, /box-sizing:\s*border-box/)
  assert.match(pageSource, /var\(--td-brand-color-7, var\(--td-brand-color\)\)/)
  assert.match(pageSource, /\.mock-page__reopen \{[\s\S]*color:\s*var\(--td-bg-color-container\)/)
  assert.match(
    pageSource,
    /\.mock-page__eyebrow \{[\s\S]*color:\s*color-mix\(in srgb, #1677ff 60%, var\(--td-text-color-primary\)\)/,
  )
  assert.doesNotMatch(pageSource, /color:\s*var\(--td-text-color-placeholder\)/)
  assert.match(pageSource, /@media \(max-width: 720px\)/)
  assert.match(pageSource, /@media \(prefers-reduced-motion: reduce\)/)
})
