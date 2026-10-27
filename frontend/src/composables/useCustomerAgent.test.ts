import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { computed, nextTick, ref, watch } from 'vue'

/**
 * 客户智能体对齐（T07 口径修正）：全项目只有 useCustomerAgent 一处为客户设置
 * selectedAgentId；`builtin-quick-answer` 不触发 toggleAgent，也不重建会话。
 */

const source = readFileSync(new URL('./useCustomerAgent.ts', import.meta.url), 'utf8')
const ast = ts.createSourceFile('useCustomerAgent.ts', source, ts.ScriptTarget.Latest, true)
const fn = ast.statements.find(
  (statement) => ts.isFunctionDeclaration(statement) && statement.name?.text === 'useCustomerAgent',
)
assert.ok(fn, 'useCustomerAgent.ts must export useCustomerAgent')
const compiled = ts.transpileModule(
  fn.getText(ast).replace(/^export\s+/, ''),
  { compilerOptions: { target: ts.ScriptTarget.ES2022 } },
).outputText

interface Harness {
  run: (isEnabled: () => boolean) => void
  settings: {
    selectedAgentId: string
    selectCalls: string[]
    toggleCalls: boolean[]
  }
  lineStoreCreations: number
  currentLine: { value: { agentId: string } | null }
}

function createHarness(initialAgentId = 'other-agent'): Harness {
  const settings = { selectedAgentId: initialAgentId, selectCalls: [] as string[], toggleCalls: [] as boolean[] }
  const settingsStore = {
    get selectedAgentId() {
      return settings.selectedAgentId
    },
    selectAgent(agentId: string) {
      settings.selectCalls.push(agentId)
      settings.selectedAgentId = agentId
    },
    toggleAgent(enabled: boolean) {
      settings.toggleCalls.push(enabled)
    },
  }
  const currentLine = ref<{ agentId: string } | null>({ agentId: 'builtin-quick-answer' })
  let lineStoreCreations = 0
  const useProductLineStore = () => {
    lineStoreCreations += 1
    return {
      // 模拟 Pinia store 对 ref 的自动解包：useCustomerAgent 读取的是 store.currentLine，不是 .value
      get currentLine() {
        return currentLine.value
      },
      get currentLineId() {
        return 'line-1'
      },
    }
  }

  const run = (isEnabled: () => boolean) => {
    const execute = runInNewContext(`${compiled}\nuseCustomerAgent`, {
      computed,
      watch,
      console,
      useProductLineStore,
      useSettingsStore: () => settingsStore,
    }) as (enabled: () => boolean) => void
    execute(isEnabled)
  }

  return {
    run,
    settings,
    get lineStoreCreations() {
      return lineStoreCreations
    },
    currentLine,
  }
}

test('when the current agent already matches, nothing is reset and selectAgent is not called', () => {
  const harness = createHarness('builtin-quick-answer')
  harness.run(() => true)
  assert.deepEqual(harness.settings.selectCalls, [])
  assert.deepEqual(harness.settings.toggleCalls, [])
})

test('a different agent is aligned to the product line without toggleAgent', () => {
  const harness = createHarness('some-old-agent')
  harness.run(() => true)
  assert.deepEqual(harness.settings.selectCalls, ['builtin-quick-answer'])
  assert.equal(harness.settings.selectedAgentId, 'builtin-quick-answer')
  // T07 口径：builtin-quick-answer 是快速问答，不调 toggleAgent(true)
  assert.deepEqual(harness.settings.toggleCalls, [])
})

test('admins (isEnabled=false) never create the product line store or touch settings', () => {
  const harness = createHarness('some-old-agent')
  harness.run(() => false)
  assert.equal(harness.lineStoreCreations, 0)
  assert.deepEqual(harness.settings.selectCalls, [])
})

test('changing the line agent re-aligns on the next tick', async () => {
  const harness = createHarness('builtin-quick-answer')
  harness.run(() => true)
  assert.deepEqual(harness.settings.selectCalls, [])

  harness.currentLine.value = { agentId: 'agent-b' }
  await nextTick()
  assert.deepEqual(harness.settings.selectCalls, ['agent-b'])
  assert.deepEqual(harness.settings.toggleCalls, [])
})

test('an empty agent id is ignored', async () => {
  const harness = createHarness('some-old-agent')
  harness.currentLine.value = null
  harness.run(() => true)
  assert.deepEqual(harness.settings.selectCalls, [])
})

test('selectAgent failures are swallowed so asking questions keeps working', () => {
  const settings = { selectedAgentId: 'old', selectCalls: 0 }
  const currentLine = ref<{ agentId: string } | null>({ agentId: 'builtin-quick-answer' })
  const settingsStore = {
    get selectedAgentId() {
      return settings.selectedAgentId
    },
    selectAgent() {
      settings.selectCalls += 1
      throw new Error('boom')
    },
  }
  const warnings: unknown[] = []
  const originalWarn = console.warn
  console.warn = (...args: unknown[]) => warnings.push(args)
  try {
    const execute = runInNewContext(`${compiled}\nuseCustomerAgent`, {
      computed,
      watch,
      console,
      useProductLineStore: () => ({
        get currentLine() {
          return currentLine.value
        },
      }),
      useSettingsStore: () => settingsStore,
    }) as (enabled: () => boolean) => void
    execute(() => true)
  } finally {
    console.warn = originalWarn
  }
  assert.equal(settings.selectCalls, 1)
  assert.ok(warnings.some((line) => String(line).includes('[customer-agent]')))
})

test('useCustomerAgent is the only customer-path caller of selectAgent and never calls toggleAgent', () => {
  assert.match(source, /settingsStore\.selectAgent\(agentId\)/)
  assert.doesNotMatch(source, /toggleAgent\s*\(/)

  const roots = [
    fileURLToPath(new URL('../components/customer/', import.meta.url)),
    fileURLToPath(new URL('./', import.meta.url)),
    fileURLToPath(new URL('../brand/', import.meta.url)),
    fileURLToPath(new URL('../stores/', import.meta.url)),
  ]
  const hits: string[] = []
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const full = join(dir, name)
      if (statSync(full).isDirectory()) {
        walk(full)
        continue
      }
      if (!/\.(ts|vue|mjs)$/.test(name) || /\.test\./.test(name)) continue
      // settings.ts 是 selectAgent/toggleAgent 的定义处，不算调用点。
      if (full.replaceAll('\\', '/').endsWith('/stores/settings.ts')) continue
      const text = readFileSync(full, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[ \t])\/\/[^\n]*/gm, '')
      if (/selectAgent\s*\(/.test(text)) hits.push(full.replaceAll('\\', '/'))
      if (/toggleAgent\s*\(/.test(text)) hits.push(`TOGGLE:${full.replaceAll('\\', '/')}`)
    }
  }
  for (const root of roots) walk(root)
  const callers = hits
    .filter((hit) => !hit.startsWith('TOGGLE:'))
    .filter((hit) => !hit.endsWith('/composables/useCustomerAgent.ts'))
  assert.deepEqual(
    callers,
    [],
    `useCustomerAgent.ts must be the only customer-path caller of selectAgent: ${callers.join(', ')}`,
  )
  const toggles = hits.filter((hit) => hit.startsWith('TOGGLE:'))
  assert.deepEqual(toggles, [], `customer paths must not call toggleAgent: ${toggles.join(', ')}`)
})
