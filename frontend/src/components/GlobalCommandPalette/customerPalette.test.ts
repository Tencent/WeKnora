import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { computed } from 'vue'

/**
 * 客户命令面板（AC-12 的入口收敛 + T11 round3 回归）：
 * 客户 Ctrl+K 只允许「新建对话」「打开设置」两条，且不出现 WeKnora 助手、知识库、
 * 智能体、组织、新手引导等管理员命令。
 *
 * 过滤逻辑写在 GlobalCommandPalette.vue 的 computed 里，这里沿用仓库已有的
 * AST 抽取做法执行真实逻辑，不为了测试去改组件。
 */

const source = readFileSync(new URL('../GlobalCommandPalette.vue', import.meta.url), 'utf8')
const useSearchSource = readFileSync(new URL('./useSearch.ts', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)![1]!
const ast = ts.createSourceFile('GlobalCommandPalette.ts', script, ts.ScriptTarget.Latest, true)

function findVariable(name: string): ts.VariableStatement {
  const statement = ast.statements.find(
    (item): item is ts.VariableStatement =>
      ts.isVariableStatement(item) &&
      item.declarationList.declarations.some(
        (declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === name,
      ),
  )
  assert.ok(statement, `GlobalCommandPalette.vue must define ${name}`)
  return statement
}

const idsStatement = findVariable('CUSTOMER_COMMAND_IDS')
const commandsStatement = findVariable('allCommands')
const compiled = ts.transpileModule(
  `${idsStatement.getText(ast)}\n${commandsStatement.getText(ast)}\n;({ CUSTOMER_COMMAND_IDS, allCommands })`,
  { compilerOptions: { target: ts.ScriptTarget.ES2022 } },
).outputText

// 与 i18n/locales/zh-CN.ts 的 commandPalette.quick 一致；客户版只应命中后两条。
const ALL_COMMANDS = [
  { id: 'open-assistant', label: '打开 WeKnora 助手', icon: 'compass' },
  { id: 'new-chat', label: '新建对话', icon: 'chat-add' },
  { id: 'open-kb-list', label: '打开知识库', icon: 'folder' },
  { id: 'open-agents', label: '打开智能体', icon: 'user-circle' },
  { id: 'open-organizations', label: '打开共享空间', icon: 'usergroup' },
  { id: 'open-settings', label: '打开设置', icon: 'setting' },
  { id: 'open-product-tour', label: '新手引导', icon: 'help-circle' },
]

function runPalette(options: {
  customer: boolean
  role?: 'viewer' | 'contributor' | 'admin' | 'owner'
  agentsSupported?: boolean
  organizationsSupported?: boolean
}): Array<{ id: string; label: string }> {
  const authStore = {
    hasRole: (min: string) => {
      const levels: Record<string, number> = { viewer: 1, contributor: 2, admin: 3, owner: 4 }
      return (levels[options.role ?? 'admin'] ?? 0) >= (levels[min] ?? 0)
    },
  }
  const deploymentCapabilities = {
    isSupported: (key: string) => {
      if (key === 'agents') return options.agentsSupported ?? false
      if (key === 'organizations') return options.organizationsSupported ?? false
      return true
    },
  }
  return runInNewContext(compiled, {
    computed,
    buildCommands: () => ALL_COMMANDS.map((command) => ({ ...command })),
    router: {},
    t: (key: string) => key,
    commandPaletteStore: { closePalette() {} },
    isCustomerViewer: () => options.customer,
    isCustomerPalette: computed(() => options.customer),
    authStore,
    deploymentCapabilities,
  }) as { CUSTOMER_COMMAND_IDS: string[]; allCommands: { value: Array<{ id: string; label: string }> } } as never
}

function customerCommands() {
  const result = runPalette({ customer: true, role: 'viewer' }) as unknown as {
    CUSTOMER_COMMAND_IDS: string[]
    allCommands: { value: Array<{ id: string; label: string }> }
  }
  return result
}

test('customer palette whitelist is exactly new-chat and open-settings', () => {
  assert.deepEqual([...customerCommands().CUSTOMER_COMMAND_IDS], ['new-chat', 'open-settings'])
})

test('customer allCommands contains only two commands and no WeKnora/assistant/admin entries', () => {
  const commands = customerCommands().allCommands.value
  assert.deepEqual(commands.map((command) => command.id), ['new-chat', 'open-settings'])
  assert.deepEqual(commands.map((command) => command.label), ['新建对话', '打开设置'])
  const text = commands.map((command) => command.label).join(' ')
  for (const forbidden of ['WeKnora', 'weknora', '助手', '知识库', '智能体', '共享空间', '新手引导']) {
    assert.equal(text.includes(forbidden), false, `customer palette must not contain: ${forbidden}`)
  }
})

test('admin palette keeps the existing commands (capabilities still gate agents/organizations)', () => {
  const commands = runPalette({ customer: false, role: 'admin', agentsSupported: true, organizationsSupported: true }) as unknown as {
    allCommands: { value: Array<{ id: string }> }
  }
  assert.deepEqual(commands.allCommands.value.map((command) => command.id), [
    'open-assistant', 'new-chat', 'open-kb-list', 'open-agents', 'open-organizations',
    'open-settings', 'open-product-tour',
  ])
})

test('non-admin admins-side palette still hides organizations', () => {
  const commands = runPalette({ customer: false, role: 'contributor', organizationsSupported: true }) as unknown as {
    allCommands: { value: Array<{ id: string }> }
  }
  assert.equal(commands.allCommands.value.some((command) => command.id === 'open-organizations'), false)
  assert.equal(commands.allCommands.value.some((command) => command.id === 'open-assistant'), true)
})

test('M11 customer palette hides retrieval tuning entry and empty-state action', () => {
  // 检索设置齿轮/弹层（top-k/阈值）属于管理员入口，客户不渲染。
  assert.match(
    source,
    /<!-- 检索微调（top-k\/阈值）属于管理员设置，客户版不渲染入口与弹层（M11 \/ AC-23） -->\s*<t-popup v-if="!isCustomerPalette"/,
  )
  // 空结果的「调整检索」按钮同样只对管理员显示。
  assert.match(source, /<t-button v-if="!isCustomerPalette" variant="outline" size="small" @click="settingsVisible = true">/)
})

test('M12 customer palette skips KB semantic search and agent preload', () => {
  assert.match(source, /knowledgeEnabled: \(\) => !isCustomerPalette\.value/)
  assert.match(source, /agentsEnabled: \(\) => !isCustomerPalette\.value/)
  // useSearch：客户不预加载 KB、不发语义检索、不预加载 agent。
  assert.match(useSearchSource, /if \(!searchKnowledge\) \{[\s\S]*?kbIds = \[\]/)
  assert.match(useSearchSource, /if \(knowledgeEnabled\(\)\) ensureKbs\(\)/)
  assert.match(useSearchSource, /if \(options\.agentsEnabled\?\.\(\) !== false\)/)
})

test('customer result groups never include KB / agent / chunk groups', () => {
  // 对象是 query 非空时的组顺序；客户只保留历史会话与消息。
  assert.match(
    source,
    /\? \['messages', 'sessions', 'commands'\] as const/,
    'customer search results must stay in the customer-allowed routes',
  )
  assert.match(source, /: \['recent', 'commands'\] as const/)
  assert.doesNotMatch(source, /customer[\s\S]{0,200}'chunks'/)
})
