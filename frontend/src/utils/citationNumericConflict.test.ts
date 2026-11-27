import assert from 'node:assert/strict'
import test from 'node:test'
import { findCitationNumericConflict } from './citationNumericConflict.ts'

test('reports a different number for the same metric and unit', () => {
  assert.deepEqual(
    findCitationNumericConflict('操作日志保留 90 天。', '操作日志保留 30 天。'),
    { answer: '90 天', source: '30 天' },
  )
  assert.deepEqual(findCitationNumericConflict('项目预算300万元。', '项目预算30万元。'), {
    answer: '300 万元',
    source: '30 万元',
  })
  assert.deepEqual(findCitationNumericConflict('Log retention is 2 days.', 'Log retention is 1 day.'), {
    answer: '2 days',
    source: '1 day',
  })
})

test('keeps the source metric separate from other metrics with or without the same number', () => {
  const source = '操作日志保留 30 天。'
  assert.equal(findCitationNumericConflict('日志页面最多展示 90 条。', source), null)
  assert.equal(findCitationNumericConflict('备份日志保留 90 天。', source), null)
  assert.deepEqual(findCitationNumericConflict('操作日志保留 90 天。', source + '备份日志保留 90 天。'), {
    answer: '90 天',
    source: '30 天',
  })
})

test('does not compare different units or explicitly qualified metrics', () => {
  assert.equal(findCitationNumericConflict('操作日志保留 90 小时。', '操作日志保留 30 天。'), null)
  assert.equal(findCitationNumericConflict('高级版操作日志保留 90 天。', '基础版操作日志保留 30 天。'), null)
  assert.equal(findCitationNumericConflict('操作日志最多保留 90 天。', '操作日志至少保留 30 天。'), null)
  assert.equal(findCitationNumericConflict('磁盘用量达到 90 MB。', '磁盘用量达到 30 GB。'), null)
})

test('compares numeric values independently of formatting', () => {
  assert.equal(findCitationNumericConflict('项目报名人数共 1,000 人。', '项目报名人数共 1000 人。'), null)
  assert.equal(findCitationNumericConflict('项目收入增长 30%。', '项目收入增长 30 %。'), null)
  assert.equal(findCitationNumericConflict('项目收入为 50 万元。', '1. 项目收入为 50.0 万元。'), null)
  assert.equal(findCitationNumericConflict('项目预算为 30 万元。', '项目预算为 300000 元。'), null)
  assert.deepEqual(findCitationNumericConflict('项目收入增长 ３０．５％。', '项目收入增长 20.5%。'), {
    answer: '30.5%',
    source: '20.5%',
  })
})

test('skips ambiguous measurements and excerpts with no directly comparable prose', () => {
  const claim = '操作日志保留 90 天。'
  assert.equal(findCitationNumericConflict(claim, ''), null)
  assert.equal(findCitationNumericConflict(claim, '操作日志保留 20 天。操作日志保留 30 天。'), null)
  assert.equal(findCitationNumericConflict(claim, '操作日志保留 20 至 30 天。'), null)
  assert.equal(findCitationNumericConflict(claim, '操作日志保留 30。'), null)
  assert.equal(findCitationNumericConflict(claim, '~~~text\n操作日志保留 30 天。\n~~~'), null)
  assert.equal(findCitationNumericConflict(claim, '示例为 `测试。操作日志保留 30 天。结束`。'), null)
  assert.equal(findCitationNumericConflict(claim + '以上为现行规则。', '操作日志保留 30 天。'), null)
  assert.deepEqual(findCitationNumericConflict(claim, '**操作日志保留** 30 天。'), {
    answer: '90 天',
    source: '30 天',
  })
})

test('competing ranges and alternatives make an otherwise matching scalar inconclusive', () => {
  const english = 'Log retention is 90 days.'
  const chinese = '操作日志保留 90 天。'
  const cases: Array<[string, string]> = [
    [english, 'Log retention is 30 days. Log retention is 90 to 120 days.'],
    [english, 'Log retention is between 90 and 120 days. Log retention is 30 days.'],
    [english, 'Log retention is 30 days. Log retention is 90 days or 120 days for archived logs.'],
    [english, 'Log retention is 30 days. Log retention: 90–120 days.'],
    [chinese, '操作日志保留 30 天。操作日志保留 90–120 天。'],
    [chinese, '操作日志保留30天。操作日志保留范围为90至120天。'],
  ]
  for (const [claim, excerpt] of cases) {
    assert.equal(findCitationNumericConflict(claim, excerpt), null, excerpt)
  }
})

test('a range for another metric leaves an unambiguous matching scalar comparable', () => {
  assert.deepEqual(findCitationNumericConflict(
    'Log retention is 90 days.',
    'Log retention is 30 days. Backup retention is 90 to 120 days.',
  ), { answer: '90 days', source: '30 days' })
})
