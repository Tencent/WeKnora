import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import type { CustomAgent, UpdateAgentRequest } from '../api/agent/index.ts'
import type { useSkillInstallerModel } from './useSkillInstallerModel.ts'

const require = createRequire(import.meta.url)
const compiled = ts.transpileModule(
  readFileSync(new URL('./useSkillInstallerModel.ts', import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText

function agent(config: CustomAgent['config'] = {}): CustomAgent {
  return { id: 'builtin-skill-installer', name: 'Installer', is_builtin: true, config }
}

function fixture(options: {
  load?: () => Promise<{ data?: CustomAgent | null }>
  save?: (data: UpdateAgentRequest) => Promise<{ data?: CustomAgent | null } | undefined>
  readStorage?: (key: string) => string | null
} = {}) {
  const reads: string[] = []
  const writes: Array<{ id: string; data: UpdateAgentRequest }> = []
  const errors: string[] = []
  const exports: { useSkillInstallerModel?: typeof useSkillInstallerModel } = {}
  runInNewContext(compiled, {
    exports,
    localStorage: { getItem: (key: string) => options.readStorage?.(key) ?? null },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { error: (message: string) => errors.push(message) } }
      if (name === '@/api/agent') return {
        getAgentById: async (id: string) => {
          reads.push(id)
          return options.load ? options.load() : { data: agent() }
        },
        updateAgent: async (id: string, data: UpdateAgentRequest) => {
          writes.push({ id, data: JSON.parse(JSON.stringify(data)) })
          return options.save ? options.save(data) : { data: { ...agent(), ...data } }
        },
      }
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  const create = exports.useSkillInstallerModel!
  return { model: create(), create, reads, writes, errors }
}

test('configured installer model takes precedence over the last chat model', async () => {
  const f = fixture({
    load: async () => ({ data: agent({ model_id: ' configured ' }) }),
    readStorage: () => { throw new Error('configured model must not read storage') },
  })
  await f.model.loadInstallerModel()
  assert.equal(f.model.installerModelId.value, 'configured')
  assert.deepEqual(f.reads, ['builtin-skill-installer'])
  assert.equal(f.writes.length, 0)
  assert.deepEqual(f.errors, [])
})

test('missing, empty and failed agent loads fall back to the last chat model without writing', async () => {
  const loads = [
    async () => ({ data: null }),
    async () => ({ data: agent() }),
    async () => ({ data: agent({ model_id: '  ' }) }),
    async () => { throw new Error('unavailable') },
  ]
  for (const load of loads) {
    const keys: string[] = []
    const f = fixture({ load, readStorage: key => { keys.push(key); return 'last-chat' } })
    await f.model.loadInstallerModel()
    assert.equal(f.model.installerModelId.value, 'last-chat')
    assert.deepEqual(keys, ['weknora_last_chat_model_id'])
    assert.equal(f.writes.length, 0)
    assert.deepEqual(f.errors, [])
  }
})

test('unavailable storage or missing preference leaves an empty selection', async () => {
  for (const readStorage of [() => null, () => { throw new Error('storage blocked') }]) {
    const f = fixture({ readStorage })
    await f.model.loadInstallerModel()
    assert.equal(f.model.installerModelId.value, '')
    assert.deepEqual(f.errors, [])
  }
})

test('saving trims the model ID and preserves agent metadata and unrelated config', async () => {
  const original = {
    ...agent({ model_id: 'old', temperature: 0.4, allowed_tools: ['shell_exec'], system_prompt: 'Keep this prompt' }),
    description: 'Description', avatar: 'avatar.png',
  }
  const f = fixture({ load: async () => ({ data: original }) })
  await f.model.loadInstallerModel()
  await f.model.persistInstallerModel(' new ')
  assert.deepEqual(f.writes, [{
    id: 'builtin-skill-installer',
    data: {
      name: 'Installer', description: 'Description', avatar: 'avatar.png',
      config: { ...original.config, model_id: 'new' },
    },
  }])
  assert.equal(f.model.installerModelId.value, 'new')
  assert.equal(original.config.model_id, 'old', 'saving must not mutate the loaded response')
})

test('subsequent saves use returned agent config, or retain the previous config when data is absent', async () => {
  for (const hasResponse of [true, false]) {
    const f = fixture({
      load: async () => ({ data: agent({ temperature: 0.4 }) }),
      save: async () => hasResponse ? { data: { ...agent({ temperature: 0.7 }), name: 'Returned name' } } : {},
    })
    await f.model.loadInstallerModel()
    await f.model.persistInstallerModel('first')
    await f.model.persistInstallerModel('second')
    assert.equal(f.writes[1]?.data.name, hasResponse ? 'Returned name' : 'Installer')
    assert.deepEqual(f.writes[1]?.data.config, { temperature: hasResponse ? 0.7 : 0.4, model_id: 'second' })
    assert.equal(f.model.installerModelId.value, 'second')
  }
})

test('a load failure clears stale agent metadata before persisting a fallback selection', async () => {
  let fail = false
  const f = fixture({
    load: async () => {
      if (fail) throw new Error('unavailable')
      return { data: agent({ model_id: 'old', system_prompt: 'old metadata' }) }
    },
    readStorage: () => 'last-chat',
    save: async () => undefined,
  })
  await f.model.loadInstallerModel()
  fail = true
  await f.model.loadInstallerModel()
  await f.model.persistInstallerModel(f.model.installerModelId.value)
  assert.deepEqual(f.writes[0]?.data, { name: '', description: '', avatar: '', config: { model_id: 'last-chat' } })
  await f.model.persistInstallerModel('next')
  assert.equal(f.writes[1]?.data.config?.model_id, 'next')
})

test('blank persistence rejects, while empty and add-model selector events do not save', async () => {
  const f = fixture()
  f.model.installerModelId.value = 'current'
  await assert.rejects(f.model.persistInstallerModel('  '), /settings.sandbox.skillInstallerModelRequired/)
  await f.model.onInstallerModelChange('')
  await f.model.onInstallerModelChange('__add_model__')
  assert.equal(f.model.installerModelId.value, 'current')
  assert.equal(f.model.savingInstallerModel.value, false)
  assert.equal(f.writes.length, 0)
  assert.deepEqual(f.errors, [])
})

test('selector save exposes loading until completion and preserves a failed draft for retry', async () => {
  let fail!: (reason: Error) => void
  let succeed!: (value: { data: CustomAgent }) => void
  const f = fixture({ save: () => new Promise((resolve, reject) => { succeed = resolve; fail = reject }) })
  const first = f.model.onInstallerModelChange('next')
  assert.equal(f.model.installerModelId.value, 'next')
  assert.equal(f.model.savingInstallerModel.value, true)
  fail(new Error('permission denied'))
  await first
  assert.equal(f.model.installerModelId.value, 'next')
  assert.equal(f.model.savingInstallerModel.value, false)
  assert.deepEqual(f.errors, ['permission denied'])
  const retry = f.model.onInstallerModelChange('next')
  assert.equal(f.model.savingInstallerModel.value, true)
  succeed({ data: agent({ model_id: 'next' }) })
  await retry
  assert.equal(f.writes.length, 2)
  assert.equal(f.model.savingInstallerModel.value, false)
})

test('selector reports a localized fallback error, while persistence propagates failure to the install caller', async () => {
  const f = fixture({ save: async () => { throw null } })
  await f.model.onInstallerModelChange('selected')
  assert.deepEqual(f.errors, ['settings.sandbox.skillInstallerModelSaveFailed'])
  const failure = new Error('save failed')
  const direct = fixture({ save: async () => { throw failure } })
  direct.model.installerModelId.value = 'old'
  await assert.rejects(direct.model.persistInstallerModel('new'), error => error === failure)
  assert.equal(direct.model.installerModelId.value, 'old')
  assert.deepEqual(direct.errors, [])
})

test('reset clears the panel selection and cached metadata without affecting a sibling instance', async () => {
  const f = fixture({ load: async () => ({ data: agent({ model_id: 'configured', temperature: 0.4 }) }) })
  const sibling = f.create()
  await f.model.loadInstallerModel()
  await sibling.loadInstallerModel()
  f.model.resetInstallerModel()
  assert.equal(f.model.installerModelId.value, '')
  assert.equal(sibling.installerModelId.value, 'configured')
  await f.model.persistInstallerModel('after-reset')
  assert.deepEqual(f.writes[0]?.data, { name: '', description: '', avatar: '', config: { model_id: 'after-reset' } })
  assert.equal(sibling.installerModelId.value, 'configured')
})
