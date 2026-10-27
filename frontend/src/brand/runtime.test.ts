import assert from 'node:assert/strict'
import test from 'node:test'
import { deriveActionColor } from './colors.ts'
import { DEFAULT_BRAND } from './defaults.ts'
import { brandConfig, productLinesFallback, readBrandConfig } from './runtime.ts'

/**
 * 品牌配置解析是客户版所有运行时可配置能力的底座（AC-1 / AC-2 / AC-8 / AC-16）。
 * 这里只测纯函数与启动单例，不碰 DOM。
 */

function captureWarnings(): { lines: string[]; restore: () => void } {
  const lines: string[] = []
  const original = console.warn
  console.warn = (...args: unknown[]) => {
    lines.push(args.map((arg) => String(arg)).join(' '))
  }
  return {
    lines,
    restore: () => {
      console.warn = original
    },
  }
}

function withoutWarnings<T>(run: () => T): T {
  const capture = captureWarnings()
  try {
    return run()
  } finally {
    capture.restore()
  }
}

const GOOD_RAW = {
  productName: '测试品牌',
  logoUrl: '/brand/test.png',
  logoWidth: 180,
  faviconUrl: '/test.ico',
  primaryColor: '#123456',
  actionColor: '#234567',
  loginSubtitle: '测试副标题',
  chatDisclaimer: '测试提示',
  productLines: [
    { id: 'line-a', name: '产线 A', description: '说明 A', agentId: 'agent-a', samples: ['q1', 'q2'] },
    { id: 'line-b', name: '产线 B', description: '说明 B', agentId: 'agent-b', samples: ['q3'] },
  ],
}

test('readBrandConfig(undefined) falls back to the complete built-in brand', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig(undefined)
  } finally {
    capture.restore()
  }
  assert.deepEqual(config, DEFAULT_BRAND)
  assert.ok(capture.lines.some((line) => line.includes('未找到') && line.includes('BRAND')))
})

test('readBrandConfig accepts a valid runtime config unchanged', () => {
  const config = withoutWarnings(() => readBrandConfig(GOOD_RAW))
  assert.equal(config.productName, '测试品牌')
  assert.equal(config.logoUrl, '/brand/test.png')
  assert.equal(config.logoWidth, 180)
  assert.equal(config.primaryColor, '#123456')
  assert.equal(config.actionColor, '#234567')
  assert.deepEqual(config.productLines.map((line) => line.id), ['line-a', 'line-b'])
})

test('missing fields fall back one by one and the warning names the field', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({ productName: '只有名字' })
  } finally {
    capture.restore()
  }
  assert.equal(config.productName, '只有名字')
  assert.equal(config.logoUrl, DEFAULT_BRAND.logoUrl)
  assert.equal(config.primaryColor, DEFAULT_BRAND.primaryColor)
  assert.deepEqual(config.productLines, DEFAULT_BRAND.productLines)
  for (const key of ['logoUrl', 'logoWidth', 'faviconUrl', 'primaryColor', 'loginSubtitle', 'chatDisclaimer']) {
    assert.ok(
      capture.lines.some((line) => line.includes(`BRAND.${key}`)),
      `warning should mention BRAND.${key}`,
    )
  }
  // actionColor 是可选覆盖项（H3）：不配不告警，按主色自动派生。
  assert.equal(config.actionColor, '#0284C7', 'default primary must derive the historical action color')
  assert.equal(
    capture.lines.some((line) => line.includes('BRAND.actionColor')),
    false,
    'omitting actionColor is normal and must not warn',
  )
})

test('invalid colors fall back to defaults with a locatable warning', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({ primaryColor: 'red', actionColor: '#12' })
  } finally {
    capture.restore()
  }
  assert.equal(config.primaryColor, DEFAULT_BRAND.primaryColor)
  assert.equal(config.actionColor, DEFAULT_BRAND.actionColor)
  assert.ok(capture.lines.some((line) => line.includes('BRAND.primaryColor') && line.includes('#RGB')))
  assert.ok(capture.lines.some((line) => line.includes('BRAND.actionColor') && line.includes('派生')))
})

test('actionColor derives from primaryColor and an explicit value still wins (H3)', () => {
  const derived = withoutWarnings(() => readBrandConfig({ primaryColor: '#7C3AED' }))
  assert.equal(derived.actionColor, deriveActionColor('#7C3AED'))
  assert.notEqual(derived.actionColor, DEFAULT_BRAND.actionColor)

  const explicit = withoutWarnings(() => readBrandConfig({ primaryColor: '#7C3AED', actionColor: '#111111' }))
  assert.equal(explicit.actionColor, '#111111')

  // 非法 actionColor 时按当前主色派生，而不是退回默认主色。
  const capture = captureWarnings()
  let invalid
  try {
    invalid = readBrandConfig({ primaryColor: '#7C3AED', actionColor: '#12' })
  } finally {
    capture.restore()
  }
  assert.equal(invalid.actionColor, deriveActionColor('#7C3AED'))
  assert.ok(capture.lines.some((line) => line.includes('BRAND.actionColor') && line.includes('派生')))
})

test('duplicate product line ids keep the first and duplicate samples collapse (L-13)', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({
      productLines: [
        { id: 'a', name: '第一条 A', agentId: 'x', samples: ['q1', 'q1', 'q2'] },
        { id: 'b', name: 'B', agentId: 'x', samples: ['q3'] },
        { id: 'a', name: '重复 A', agentId: 'x', samples: ['q4'] },
      ],
    })
  } finally {
    capture.restore()
  }
  assert.deepEqual(config.productLines.map((line) => line.id), ['a', 'b'])
  assert.equal(config.productLines[0]?.name, '第一条 A', 'the first occurrence wins')
  assert.deepEqual(config.productLines[0]?.samples, ['q1', 'q2'])
  assert.ok(capture.lines.some((line) => line.includes('id「a」重复')))
  assert.ok(capture.lines.some((line) => line.includes('samples') && line.includes('重复')))
})

test('3-digit colors are accepted and logoWidth accepts numeric strings', () => {
  const config = withoutWarnings(() => readBrandConfig({ primaryColor: '#abc', actionColor: '#def', logoWidth: '168' }))
  assert.equal(config.primaryColor, '#abc')
  assert.equal(config.actionColor, '#def')
  assert.equal(config.logoWidth, 168)
})

test('invalid logoWidth falls back and warns', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({ logoWidth: -3 })
  } finally {
    capture.restore()
  }
  assert.equal(config.logoWidth, DEFAULT_BRAND.logoWidth)
  assert.ok(capture.lines.some((line) => line.includes('BRAND.logoWidth')))
})

test('product lines missing id/name/agentId are dropped, valid ones survive', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({
      productLines: [
        { id: 'ok', name: '保留', agentId: 'agent-ok', description: 'd', samples: ['s1', 's2'] },
        { id: '', name: '缺 id', agentId: 'x' },
        { id: 'no-agent', name: '缺 agent' },
        { id: 'blank-name', name: '   ', agentId: 'x' },
      ],
    })
  } finally {
    capture.restore()
  }
  assert.deepEqual(config.productLines.map((line) => line.id), ['ok'])
  assert.equal(config.productLines[0]?.samples.length, 2)
  assert.ok(capture.lines.some((line) => line.includes('productLines[1]') && line.includes('id')))
  assert.ok(capture.lines.some((line) => line.includes('productLines[2]') && line.includes('agentId')))
})

test('all-invalid product lines fall back to the two defaults', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({ productLines: [{}, { id: 'x' }] })
  } finally {
    capture.restore()
  }
  assert.deepEqual(config.productLines, DEFAULT_BRAND.productLines)
  assert.ok(capture.lines.some((line) => line.includes('没有可用产线') && line.includes('默认产线')))
})

test('non-array productLines and non-object brand fall back to defaults', () => {
  withoutWarnings(() => {
    assert.deepEqual(readBrandConfig({ productLines: 'oops' }).productLines, DEFAULT_BRAND.productLines)
    assert.deepEqual(readBrandConfig('oops'), DEFAULT_BRAND)
    assert.deepEqual(readBrandConfig([]), DEFAULT_BRAND)
  })
})

test('samples keep only non-empty strings and drop junk with a warning', () => {
  const capture = captureWarnings()
  let config
  try {
    config = readBrandConfig({
      productLines: [{ id: 'a', name: 'A', agentId: 'x', samples: ['keep', '', 42, '  trim  '] }],
    })
  } finally {
    capture.restore()
  }
  assert.deepEqual(config.productLines[0]?.samples, ['keep', 'trim'])
  assert.ok(capture.lines.some((line) => line.includes('samples')))
})

test('the returned config is a clone: mutating it never touches DEFAULT_BRAND', () => {
  const config = withoutWarnings(() => readBrandConfig(undefined))
  config.productName = '改了'
  config.productLines[0]!.name = '改了'
  config.productLines[0]!.samples.push('新问题')
  assert.equal(DEFAULT_BRAND.productName, '小爱知识库')
  assert.equal(DEFAULT_BRAND.productLines[0]?.name, '金蝶 AI 套件')
  assert.equal(DEFAULT_BRAND.productLines[0]?.samples.length, 2)
})

test('startup brandConfig is deeply frozen (top level, lines, samples)', () => {
  assert.equal(brandConfig.productName, DEFAULT_BRAND.productName)
  assert.ok(Object.isFrozen(brandConfig))
  assert.ok(Object.isFrozen(brandConfig.productLines))
  assert.ok(Object.isFrozen(brandConfig.productLines[0]))
  assert.ok(Object.isFrozen(brandConfig.productLines[0]?.samples))
  assert.throws(() => {
    ;(brandConfig as { productName: string }).productName = 'x'
  }, TypeError)
  assert.throws(() => {
    brandConfig.productLines[0]?.samples.push('x')
  }, TypeError)
})

test('both default product lines share builtin-quick-answer (A2)', () => {
  assert.equal(DEFAULT_BRAND.productLines.length, 2)
  for (const line of DEFAULT_BRAND.productLines) {
    assert.equal(line.agentId, 'builtin-quick-answer')
    assert.ok(line.samples.length >= 2 && line.samples.length <= 3, 'each line keeps 2-3 sample questions')
  }
})

test('productLinesFallback is false for a healthy default startup', () => {
  assert.equal(productLinesFallback, false)
})

async function importRuntimeWithBrand(caseName: string): Promise<typeof import('./runtime.ts')> {
  return import(`./runtime.ts?case=${caseName}`)
}

test('productLinesFallback is true when startup config has no usable line', async () => {
  const originalWindow = (globalThis as { window?: unknown }).window
  ;(globalThis as { window?: unknown }).window = {
    __RUNTIME_CONFIG__: { BRAND: { productLines: [{ id: '', name: '' }] } },
  }
  const capture = captureWarnings()
  try {
    const mod = await importRuntimeWithBrand('fallback-all-bad')
    assert.equal(mod.productLinesFallback, true)
    assert.equal(mod.brandConfig.productLines.length, 2)
  } finally {
    capture.restore()
    if (originalWindow === undefined) delete (globalThis as { window?: unknown }).window
    else (globalThis as { window?: unknown }).window = originalWindow
  }
})

test('productLinesFallback is true when startup config omits productLines', async () => {
  const originalWindow = (globalThis as { window?: unknown }).window
  ;(globalThis as { window?: unknown }).window = {
    __RUNTIME_CONFIG__: { BRAND: { productName: '仅名字' } },
  }
  const capture = captureWarnings()
  try {
    const mod = await importRuntimeWithBrand('fallback-missing')
    assert.equal(mod.productLinesFallback, true)
  } finally {
    capture.restore()
    if (originalWindow === undefined) delete (globalThis as { window?: unknown }).window
    else (globalThis as { window?: unknown }).window = originalWindow
  }
})

test('productLinesFallback is false when startup config has one usable line', async () => {
  const originalWindow = (globalThis as { window?: unknown }).window
  ;(globalThis as { window?: unknown }).window = {
    __RUNTIME_CONFIG__: {
      BRAND: { productLines: [{ id: 'only', name: '唯一产线', agentId: 'agent-1', samples: ['q'] }] },
    },
  }
  const capture = captureWarnings()
  try {
    const mod = await importRuntimeWithBrand('fallback-ok')
    assert.equal(mod.productLinesFallback, false)
    assert.deepEqual(mod.brandConfig.productLines.map((line) => line.id), ['only'])
  } finally {
    capture.restore()
    if (originalWindow === undefined) delete (globalThis as { window?: unknown }).window
    else (globalThis as { window?: unknown }).window = originalWindow
  }
})
