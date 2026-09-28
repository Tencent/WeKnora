import assert from 'node:assert/strict'
import test from 'node:test'
import { findTableSource } from './tableSource'
import { resolvePdfSource } from './pdfSourceLocate'

const courses = '数据实验 数据实验 数据实验 音乐创编（初级） 舞蹈 A 团（周一） 天文观测'
const teachers = '李欣 何逸群 张健 袁亦佳 周孟影 刘佳文'
const scope = `|五 年 级|周一 周二|${courses}|${teachers}|`
const original = '五年级 综合课程表\n数据实验 李欣 教室\n数据实验 何逸群 教室\n数据实验 张健 教室\n音乐创编（初级） 袁亦佳 音乐室\n舞蹈 A 团（周一） 周孟影 礼堂\n天文观测 刘佳文 科学室'

test('paired column sequences resolve repeated courses to their original rows', () => {
  const nearbyGrade = original.replace('五年级', '六年级').replace('何逸群', '杨群英')
  const hits = findTableSource([nearbyGrade, original, nearbyGrade], scope)
  assert.equal(hits.length, 12)
  assert.ok(hits.every(h => h.page === 2))
  assert.equal(hits.filter(h => h.quote === '数据实验').length, 3)
  assert.ok(hits.some(h => h.quote.includes('舞蹈 A 团')))
})

test('identical tables in two places remain unresolved', () => {
  assert.deepEqual(findTableSource([original, original], scope), [])
})

test('matching names in the wrong rows cannot satisfy paired columns', () => {
  assert.deepEqual(findTableSource([original.replace('数据实验 李欣', '数据实验 袁亦佳').replace('初级） 袁亦佳', '初级） 李欣')], scope), [])
})

test('a spanning label cannot resolve to another grade with the same courses and teachers', () => {
  assert.deepEqual(findTableSource([original.replace('五年级', '六年级')], scope), [])
})

test('individual cell fallback cannot bypass the table grade constraint', async () => {
  const wrong = original.replace('五年级', '六年级')
  const result = await resolvePdfSource({ token: 1, locators: [], quotes: [], scope }, 1, async () => wrong)
  assert.deepEqual(result.targets, [])
  assert.equal(result.reason, 'missing')
})

test('a sequence cannot splice rows across an intervening mismatched table row', () => {
  const lines = original.split('\n')
  lines.splice(3, 0, '数据实验 李欣 教室')
  assert.deepEqual(findTableSource([lines.join('\n')], scope), [])
})

test('ordered rows can cross a PDF page boundary', () => {
  const lines = original.split('\n')
  const hits = findTableSource([lines.slice(0, 4).join('\n'), lines.slice(4).join('\n')], scope)
  assert.equal(hits.length, 12)
  assert.deepEqual([...new Set(hits.map(h => h.page))], [1, 2])
})

test('a changed numeric cell is not accepted as a prefix of the original', () => {
  const numerical = '|检测一批（1.5） 检测二批（2.5） 检测三批（3.5） 检测四批（4.5）|负责人甲 负责人乙 负责人丙 负责人丁|'
  const page = '检测一批（15） 负责人甲\n检测二批（2.5） 负责人乙\n检测三批（3.5） 负责人丙\n检测四批（4.5） 负责人丁'
  assert.deepEqual(findTableSource([page], numerical), [])
})

test('image-heavy steps recover exact instructions around font arrows and page gaps', async () => {
  const text = '第二步：设置默认页面并点击确认按钮。\n第三步：进入桌面打开选修课应用。\na) 首页进入【选修课】à②左上角【选课报名】à③滑动找到待选课程，单击【申请选修】按钮\n' + '图片识别产生的额外说明文字'.repeat(30)
  const pages = ['第二步：设置默认页面并点击确认按钮。', '图片内容\n第三步：进入桌面打开选修课应用。\na) 首页进入【选修课】→②左上角【选课报名】→③滑动找到待选课程，单击【申请选修】按钮']
  const r = await resolvePdfSource({token: 1, locators: [], quotes: [], scope: text, sentence: '进入【选修课】，找到课程后点击【申请选修】。'}, 2, async p => pages[p-1]!)
  assert.equal(r.reason, 'partial')
  assert.equal(r.targets[0]?.page, 2)
  assert.ok(r.targets.some(t => t.quote?.includes('申请选修')))
  assert.ok(r.targets.every(t => !t.quote?.includes('额外说明')))
})
