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
  assert.match(pageSource, /下方回执仅证明界面已触发保存事件/)
  assert.match(pageSource, /界面保存回执/)
  assert.match(pageSource, /钉钉产品使用手册/)
  assert.match(pageSource, /钉钉产品协作空间/)

  assert.match(serverSource, /name: '钉钉产品协作空间'/)
  assert.match(serverSource, /name: '使用指南'/)
  assert.match(serverSource, /name: '钉钉产品使用手册'/)
})

test('钉钉模拟演示不再显示原有英文文案', () => {
  for (const legacyText of [
    'Development-only mock E2E',
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
