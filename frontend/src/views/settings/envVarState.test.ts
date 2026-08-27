import assert from 'node:assert/strict'
import test from 'node:test'

import type { ConfigEnvGroup, EnvVarView, SkillEnvGroup } from '@/api/env-vars'
import {
  MAX_ENV_VALUE_BYTES,
  MAX_USER_ENV_VARS_PER_SCOPE,
  RESERVED_ENV_NAMES,
  addSkillEnvSaveInFlight,
  adminSkillEnvClearPayload,
  canAddEnvVar,
  canClearAdminSkillEnv,
  clearSkillEnvSaveInFlight,
  clearSubmittedSkillEnvDrafts,
  configLabel,
  editedSkillEnvPayload,
  isSkillEnvSaveInFlight,
  isValidEnvName,
  isValidEnvValueLength,
  skillHasDeclaredEnvs,
  sortedConfigGroups,
  statusOf,
} from './envVarState'

function envVar(overrides: Partial<EnvVarView> & { name: string }): EnvVarView {
  return { source: 'unset', ...overrides }
}

function skill(skillId: string, skillName: string, vars: EnvVarView[]): SkillEnvGroup {
  return { skill_id: skillId, skill_name: skillName, vars }
}

function config(
  configId: string,
  configName: string,
  vars: EnvVarView[] = [],
  skills: SkillEnvGroup[] = [],
): ConfigEnvGroup {
  return {
    sandbox_config_id: configId,
    sandbox_config_name: configName,
    vars,
    skills,
  }
}

test('statusOf reports which of the three sources supplies the value', () => {
  assert.deepEqual(statusOf(envVar({ name: 'A', source: 'unset' })), {
    key: 'unset',
    blocking: false,
  })
  assert.deepEqual(statusOf(envVar({ name: 'A', source: 'workspace' })), {
    key: 'workspace',
    blocking: false,
  })
  assert.deepEqual(statusOf(envVar({ name: 'A', source: 'user' })), {
    key: 'user',
    blocking: false,
  })
})

test('statusOf blocks only when a required variable has no value at all', () => {
  assert.equal(statusOf(envVar({ name: 'A', source: 'unset', required: true })).blocking, true)
  // A workspace value satisfies a required variable: the skill can run.
  assert.equal(statusOf(envVar({ name: 'A', source: 'workspace', required: true })).blocking, false)
  assert.equal(statusOf(envVar({ name: 'A', source: 'user', required: true })).blocking, false)
  // Optional and unset is a normal state, not a problem to flag.
  assert.equal(statusOf(envVar({ name: 'A', source: 'unset', required: false })).blocking, false)
  assert.equal(statusOf(envVar({ name: 'A', source: 'unset' })).blocking, false)
})

test('configLabel falls back to the config id when the name is blank', () => {
  assert.equal(configLabel(config('cfg-1', 'Production')), 'Production')
  assert.equal(configLabel(config('cfg-1', '   ')), 'cfg-1')
  assert.equal(configLabel(config('cfg-1', '')), 'cfg-1')
})

test('sortedConfigGroups orders by config name', () => {
  const groups = [config('c1', 'Staging'), config('c2', 'Production')]

  assert.deepEqual(
    sortedConfigGroups(groups).map((g) => g.sandbox_config_name),
    ['Production', 'Staging'],
  )
})

test('sortedConfigGroups falls back to the id so equal names keep a stable order', () => {
  const groups = [config('c2', 'Same'), config('c1', 'Same')]

  assert.deepEqual(
    sortedConfigGroups(groups).map((g) => g.sandbox_config_id),
    ['c1', 'c2'],
  )
})

test('sortedConfigGroups does not mutate its input', () => {
  const groups = [config('c1', 'Staging'), config('c2', 'Production')]
  const before = groups.map((g) => g.sandbox_config_id)

  sortedConfigGroups(groups)

  assert.deepEqual(groups.map((g) => g.sandbox_config_id), before)
})

test('isValidEnvName accepts UPPER_SNAKE_CASE names', () => {
  assert.equal(isValidEnvName('MY_KEY'), true)
  assert.equal(isValidEnvName('K'), true)
  assert.equal(isValidEnvName('_LEADING'), true)
  assert.equal(isValidEnvName('A1_B2'), true)
})

test('isValidEnvName rejects what the server rejects', () => {
  assert.equal(isValidEnvName('PATH'), false)
  assert.equal(isValidEnvName('LD_PRELOAD'), false)
  assert.equal(isValidEnvName('WEKNORA_ANYTHING'), false)
  assert.equal(isValidEnvName('my_key'), false)
  assert.equal(isValidEnvName('MY KEY'), false)
  assert.equal(isValidEnvName(''), false)
  assert.equal(isValidEnvName('1KEY'), false)
  assert.equal(isValidEnvName('MY-KEY'), false)
  assert.equal(isValidEnvName(`A${'B'.repeat(128)}`), false)
})

test('RESERVED_ENV_NAMES mirrors the sandbox reserved list', () => {
  assert.deepEqual([...RESERVED_ENV_NAMES].sort(), [
    'HOME',
    'LD_LIBRARY_PATH',
    'LD_PRELOAD',
    'NODE_OPTIONS',
    'PATH',
    'PYTHONHOME',
    'PYTHONPATH',
    'SHELL',
    'USER',
  ])
})

test('isValidEnvValueLength measures UTF-8 bytes, not JavaScript characters', () => {
  assert.equal(isValidEnvValueLength('x'.repeat(MAX_ENV_VALUE_BYTES)), true)
  assert.equal(isValidEnvValueLength('x'.repeat(MAX_ENV_VALUE_BYTES + 1)), false)

  const nonAscii = '你'.repeat(3000)
  assert.equal(nonAscii.length < MAX_ENV_VALUE_BYTES, true)
  assert.equal(isValidEnvValueLength(nonAscii), false)
})

test('canAddEnvVar allows overwrites at the per-scope limit but rejects a new name', () => {
  const existing = Array.from({ length: MAX_USER_ENV_VARS_PER_SCOPE }, (_, index) =>
    envVar({ name: `KEY_${index}`, source: 'user' }),
  )

  assert.equal(canAddEnvVar(existing, 'KEY_0'), true)
  assert.equal(canAddEnvVar(existing, 'NEW_KEY'), false)
})

test('canAddEnvVar counts only values owned by the current user', () => {
  const existing = [
    ...Array.from({ length: MAX_USER_ENV_VARS_PER_SCOPE - 1 }, (_, index) =>
      envVar({ name: `KEY_${index}`, source: 'user' }),
    ),
    envVar({ name: 'WORKSPACE_KEY', source: 'workspace' }),
    envVar({ name: 'UNSET_KEY', source: 'unset' }),
  ]

  assert.equal(canAddEnvVar(existing, 'NEW_KEY'), true)
})

test('adminSkillEnvClearPayload sends an empty string so the declaration stays', () => {
  assert.deepEqual(adminSkillEnvClearPayload('API_TOKEN'), { API_TOKEN: '' })
})

test('canClearAdminSkillEnv is only true once a workspace value is stored', () => {
  assert.equal(canClearAdminSkillEnv({ is_set: true }), true)
  assert.equal(canClearAdminSkillEnv({ is_set: false }), false)
  assert.equal(canClearAdminSkillEnv({}), false)
})

test('skillHasDeclaredEnvs is true only when the installer declared variables', () => {
  assert.equal(skillHasDeclaredEnvs({}), false)
  assert.equal(skillHasDeclaredEnvs({ envs: null }), false)
  assert.equal(skillHasDeclaredEnvs({ envs: [] }), false)
  assert.equal(skillHasDeclaredEnvs({ envs: [{ name: 'WEKNORA_API_KEY' }] }), true)
})

test('editedSkillEnvPayload includes only drafted names declared by the skill', () => {
  assert.deepEqual(
    editedSkillEnvPayload(['TOKEN', 'EMPTY'], {
      TOKEN: 'new-token',
      EMPTY: '',
      UNDECLARED: 'must-not-submit',
    }),
    { TOKEN: 'new-token', EMPTY: '' },
  )
})

test('editedSkillEnvPayload excludes untouched declared inputs so they cannot be cleared', () => {
  assert.deepEqual(editedSkillEnvPayload(['TOKEN', 'PASSWORD'], { TOKEN: 'new-token' }), {
    TOKEN: 'new-token',
  })
})

test('clearSubmittedSkillEnvDrafts clears only unchanged submitted values', () => {
  assert.deepEqual(
    clearSubmittedSkillEnvDrafts(
      {
        TOKEN: 'newer-token',
        EMPTY: '',
        OTHER: 'typed-later',
      },
      {
        TOKEN: 'submitted-token',
        EMPTY: '',
      },
    ),
    {
      TOKEN: 'newer-token',
      OTHER: 'typed-later',
    },
  )
})

test('skill env save in-flight state is scoped by config and skill', () => {
  const inFlight = addSkillEnvSaveInFlight({}, 'cfg-a', 'skill-s')

  assert.equal(isSkillEnvSaveInFlight(inFlight, 'cfg-a', 'skill-s'), true)
  assert.equal(isSkillEnvSaveInFlight(inFlight, 'cfg-a', 'other-skill'), false)
  assert.equal(isSkillEnvSaveInFlight(inFlight, 'cfg-b', 'skill-s'), false)
})

test('skill env save completion removes exactly its own in-flight entry', () => {
  const inFlight = addSkillEnvSaveInFlight(
    addSkillEnvSaveInFlight({}, 'cfg-a', 'skill-s'),
    'cfg-b',
    'skill-t',
  )

  const remaining = clearSkillEnvSaveInFlight(inFlight, 'cfg-a', 'skill-s')

  assert.equal(isSkillEnvSaveInFlight(remaining, 'cfg-a', 'skill-s'), false)
  assert.equal(isSkillEnvSaveInFlight(remaining, 'cfg-b', 'skill-t'), true)
})
