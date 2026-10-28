import assert from 'node:assert/strict'
import test from 'node:test'

import {
  API_EXTERNAL_USER_SESSION_OWNER_PREFIX,
  EMBED_SESSION_MARKER_PREFIX,
  HISTORY_LINE_GROUP_ID,
  classifyDateBucket,
  classifyLineDateBucket,
  configuredPlatforms,
  groupSessions,
  groupSessionsByProductLine,
  groupSessionsBySource,
  resolveSessionOrigin,
  shouldShowSessionSourceBadge,
} from './sessionGrouping.ts'

const sourceLabels = {
  web: 'Web',
  api: 'API',
  embedFallback: 'Embed',
  embedChannel: (id: string) => `Embed ${id}`,
  imPlatform: (p: string) => `IM ${p}`,
}

test('resolveSessionOrigin distinguishes web, IM, and embed sessions', () => {
  assert.deepEqual(resolveSessionOrigin({ id: '1' }), { kind: 'web' })
  assert.deepEqual(resolveSessionOrigin({ id: '2', im_platform: 'feishu' }), {
    kind: 'im',
    platform: 'feishu',
  })
  assert.deepEqual(
    resolveSessionOrigin({
      id: '3',
      description: `${EMBED_SESSION_MARKER_PREFIX}ch-1`,
    }),
    { kind: 'embed', channelId: 'ch-1' },
  )
  assert.deepEqual(
    resolveSessionOrigin({ id: '4', user_id: 'api_tenant_key:1:10' }),
    { kind: 'api' },
  )
  assert.deepEqual(
    resolveSessionOrigin({ id: '5', user_id: `${API_EXTERNAL_USER_SESSION_OWNER_PREFIX}1:alice` }),
    { kind: 'api' },
  )
})

test('configuredPlatforms returns distinct platform keys in first-seen order', () => {
  const channels = [
    { platform: 'feishu' },
    { platform: 'wecom' },
    { platform: 'feishu' },
    { platform: '' },
  ]
  assert.deepEqual(configuredPlatforms(channels), ['feishu', 'wecom'])
})

test('groupSessionsBySource orders web, configured IM, then embed channels', () => {
  const sessions = [
    { id: 'w', title: 'web chat' },
    { id: 'f', title: 'feishu', im_platform: 'feishu' },
    { id: 'e', title: 'embed', description: `${EMBED_SESSION_MARKER_PREFIX}ec-1` },
  ]
  const groups = groupSessionsBySource(sessions, sourceLabels, { 'ec-1': 'Help widget' }, ['feishu'], 'Pinned')
  assert.deepEqual(
    groups.map((g) => g.key),
    ['web', 'im:feishu', 'embed:ec-1'],
  )
  assert.equal(groups[2]?.label, 'Help widget')
})

test('groupSessions date mode keeps pinned sessions in their own bucket', () => {
  const now = new Date().toISOString()
  const groups = groupSessions(
    'date',
    [
      { id: 'p', is_pinned: true, updated_at: now },
      { id: 't', updated_at: now },
    ],
    {
      pinnedLabel: 'Pinned',
      bucketLabels: {
        pinned: 'Pinned',
        today: 'Today',
        yesterday: 'Yesterday',
        last7Days: '7d',
        last30Days: '30d',
        lastYear: 'Year',
        earlier: 'Earlier',
      },
      categorizeDate: () => 'today',
      sourceLabels,
      embedChannelNames: {},
      configuredImPlatforms: [],
    },
  )
  assert.deepEqual(
    groups.map((g) => g.key),
    ['pinned', 'today'],
  )
})

test('classifyDateBucket buckets recent sessions as today', () => {
  assert.equal(classifyDateBucket(new Date().toISOString()), 'today')
})

test('shouldShowSessionSourceBadge is off when channels use separate folders', () => {
  assert.equal(shouldShowSessionSourceBadge('date'), false)
  assert.equal(shouldShowSessionSourceBadge('none'), false)
})

// ---- 客户侧栏两级分组（customer-model-lines AC-16）----
// 固定 now 避免跨日界抖动：日期都基于 now 的本地日界构造。
const NOW = new Date(2026, 9, 28, 12, 0, 0)
const at = (dayOffset: number, h = 10, m = 0) =>
  new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() + dayOffset, h, m).toISOString()

test('classifyLineDateBucket 三档 + 日界边界：今天零点归 today、昨天零点归 yesterday、更早落 earlier', () => {
  assert.equal(classifyLineDateBucket(at(0, 0, 0), NOW), 'today') // 今天 00:00 边界
  assert.equal(classifyLineDateBucket(at(0, 23, 59), NOW), 'today')
  assert.equal(classifyLineDateBucket(at(-1, 0, 0), NOW), 'yesterday') // 昨天 00:00 边界
  assert.equal(classifyLineDateBucket(at(-1, 23, 59), NOW), 'yesterday')
  assert.equal(classifyLineDateBucket(at(-2, 23, 59), NOW), 'earlier') // 前天及更早
  assert.equal(classifyLineDateBucket(at(-30, 8, 0), NOW), 'earlier')
  assert.equal(classifyLineDateBucket(undefined, NOW), 'earlier') // 缺日期
  assert.equal(classifyLineDateBucket('', NOW), 'earlier')
  assert.equal(classifyLineDateBucket('not-a-date', NOW), 'earlier') // 坏日期
  assert.equal(classifyLineDateBucket(at(1, 9, 0), NOW), 'today') // 未来时刻不异常，仍归 today
})

test('groupSessionsByProductLine：组序随 config、空组隐藏、历史组固定末尾、pinned 最前', () => {
  const lines = [
    { id: 'line-a', name: '产线A' },
    { id: 'line-b', name: '产线B' },
    { id: 'line-c', name: '产线C' }, // 无会话，整组不输出
  ]
  const groups = groupSessionsByProductLine(
    [
      { id: 's-a2', product_line: 'line-a', updated_at: at(-1, 9) }, // 昨天桶
      { id: 's-a1', product_line: 'line-a', updated_at: at(0, 9) }, // 今天桶
      { id: 's-a0', product_line: 'line-a', is_pinned: true, updated_at: at(0, 8) }, // 置顶桶
      { id: 's-b1', product_line: 'line-b', updated_at: at(-3, 9) }, // 更早桶
      { id: 's-h1', updated_at: at(0, 7) }, // 缺键 → 历史
      { id: 's-h2', product_line: '', updated_at: at(-1, 7) }, // 空串 → 历史
      { id: 's-h3', product_line: 'ghost', updated_at: at(0, 7) }, // 未知 id → 历史
    ],
    lines,
    NOW,
  )
  assert.deepEqual(
    groups.map((g) => g.lineId),
    ['line-a', 'line-b', HISTORY_LINE_GROUP_ID],
    '空产线组隐藏，历史组固定末尾，组序 = lines 传入顺序',
  )
  assert.deepEqual(groups[0].label, '产线A')
  assert.equal(groups[0].isHistory, false)
  // 组内桶序 pinned → today → yesterday → earlier，桶内保持传入顺序
  assert.deepEqual(groups[0].pinned.map((s) => s.id), ['s-a0'])
  assert.deepEqual(groups[0].today.map((s) => s.id), ['s-a1'])
  assert.deepEqual(groups[0].yesterday.map((s) => s.id), ['s-a2'])
  assert.deepEqual(groups[0].earlier.map((s) => s.id), [])
  assert.deepEqual(groups[1].earlier.map((s) => s.id), ['s-b1'])
  // 历史组同样分桶，三种「无标记」都归这里
  const history = groups[2]
  assert.equal(history.isHistory, true)
  assert.equal(history.label, '')
  assert.deepEqual(history.today.map((s) => s.id), ['s-h1', 's-h3'])
  assert.deepEqual(history.yesterday.map((s) => s.id), ['s-h2'])
})

test('groupSessionsByProductLine：历史组内置顶也进 pinned 桶；空输入返回空数组', () => {
  const groups = groupSessionsByProductLine(
    [{ id: 'hp', is_pinned: true }, { id: 'h1' }],
    [{ id: 'line-a', name: '产线A' }],
    NOW,
  )
  assert.deepEqual(groups.map((g) => g.lineId), [HISTORY_LINE_GROUP_ID])
  assert.deepEqual(groups[0].pinned.map((s) => s.id), ['hp'])
  assert.deepEqual(groups[0].earlier.map((s) => s.id), ['h1']) // 无日期落 earlier
  assert.deepEqual(groupSessionsByProductLine([], [{ id: 'line-a', name: '产线A' }], NOW), [])
  // 只有历史会话时仅输出历史组；lines 为空时产线会话也归历史（防御口径）
  const onlyHistory = groupSessionsByProductLine([{ id: 'h1' }], [], NOW)
  assert.deepEqual(onlyHistory.map((g) => g.lineId), [HISTORY_LINE_GROUP_ID])
})
