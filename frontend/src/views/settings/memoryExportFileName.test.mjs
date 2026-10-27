import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

/**
 * 客户导出记忆的文件名派生（L1 / C5 / C6）。
 *
 * memoryExportFileName 不是导出函数，这里从 MemorySettings.vue 源码切片出
 * 函数本体，用桩替掉角色与品牌两个依赖后真实执行（不重写被测逻辑）：
 * - 客户：品牌产品名派生 slug + 当天日期（本地时区）；
 * - 管理员：保持 weknora-memories.json 原名（C6）；
 * - slug 不写死拼音之类罗马化字面量（C5，品牌值运行时可配）。
 */

const source = readFileSync(new URL('./MemorySettings.vue', import.meta.url), 'utf8')

const fnMatch = source.match(/const memoryExportFileName = \(\): string => \{[\s\S]*?\n\}/)
assert.ok(fnMatch, 'MemorySettings.vue must define memoryExportFileName')
// 源码是 TS，new Function 只认 JS：剥掉返回类型标注，其余逐字保留。
const fnSource = fnMatch[0].replace('const memoryExportFileName = (): string => {', 'const memoryExportFileName = () => {')
assert.ok(fnSource !== fnMatch[0], 'memoryExportFileName header changed; update this harness')
// 日期来源固定为注入值，保证断言不随跑测时刻漂移；实现若改了这行，这里立刻报错。
assert.ok(fnSource.includes('const now = new Date()'), 'memoryExportFileName must read the date via new Date()')
const fnWithFixedNow = fnSource.replace('const now = new Date()', 'const now = fixedNow')

const makeFileName = new Function(
  'isCustomer',
  'productName',
  'fixedNow',
  `
  const isCustomerViewer = () => isCustomer;
  const useAuthStore = () => null;
  const brandConfig = { productName };
  ${fnWithFixedNow}
  return memoryExportFileName();
  `,
)

const fileNameFor = (isCustomer, productName, fixedNow = new Date(2026, 9, 27)) =>
  makeFileName(isCustomer, productName, fixedNow)

test('admin export keeps the original weknora-memories.json name (L1/C6)', () => {
  assert.equal(fileNameFor(false, '小爱知识库'), 'weknora-memories.json')
  assert.equal(fileNameFor(false, 'XiaoAi Knowledge_Base!'), 'weknora-memories.json')
})

test('customer export derives the file name from the brand product name (L1)', () => {
  // 中文产品名保留原字符，仅拉丁部分转小写。
  assert.equal(fileNameFor(true, '小爱知识库'), '小爱知识库-memories-2026-10-27.json')
  // 空白/下划线转连字符，非法字符剔除，连续连字符折叠。
  assert.equal(fileNameFor(true, 'XiaoAi Knowledge_Base!'), 'xiaoai-knowledge-base-memories-2026-10-27.json')
  // 拿不到有效字符时回退 memories，保证文件名始终可用。
  assert.equal(fileNameFor(true, '!!!'), 'memories-memories-2026-10-27.json')
  assert.equal(fileNameFor(true, '   '), 'memories-memories-2026-10-27.json')
})

test('date stamp follows the local calendar day', () => {
  // 本地日历日（1 月 9 日 → 01-09），月/日两位补零。
  assert.equal(fileNameFor(true, '小爱知识库', new Date(2027, 0, 9)), '小爱知识库-memories-2027-01-09.json')
})

test('handleExport wires the derived name into the download link', () => {
  assert.match(source, /link\.download = memoryExportFileName\(\)/)
  // weknora-memories.json 只允许出现在管理员分支的 return，客户文件名完全由品牌配置派生。
  assert.equal((source.match(/return 'weknora-memories\.json'/g) ?? []).length, 1)
  assert.doesNotMatch(source, /xiaoi/)
  assert.match(source, /import \{ brandConfig \} from '@\/brand'/)
  assert.match(source, /import \{ isCustomerViewer \} from '@\/config\/customerAccess'/)
})
