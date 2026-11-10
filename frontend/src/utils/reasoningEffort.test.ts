import assert from 'node:assert/strict'
import test from 'node:test'

import {
  FALLBACK_REASONING_LEVELS,
  canonicalLevels,
  clampLevel,
  levelDescriptionKey,
  levelEnablesThinking,
  levelFromLegacy,
  levelLabelKey,
  modelCanThink,
  modelCannotDisableThinking,
  optionsFor,
  supportedLevels,
} from './reasoningEffort.ts'

test('levelFromLegacy maps the boolean when no effort is stored', () => {
  assert.equal(levelFromLegacy(true), 'auto')
  assert.equal(levelFromLegacy(false), 'off')
  assert.equal(levelFromLegacy(undefined), 'off')
  assert.equal(levelFromLegacy(null, ''), 'off')
  assert.equal(levelFromLegacy(true, '   '), 'auto')
})

test('levelFromLegacy prefers a valid effort over the boolean', () => {
  assert.equal(levelFromLegacy(false, 'high'), 'high')
  assert.equal(levelFromLegacy(true, 'off'), 'off')
  assert.equal(levelFromLegacy(true, ' Medium '), 'medium')
  assert.equal(levelFromLegacy(true, 'bogus'), 'auto', 'unknown effort falls back to the boolean')
  assert.equal(levelFromLegacy(false, 'bogus'), 'off')
})

test('levelLabelKey and levelDescriptionKey point at the model.reasoning namespace', () => {
  assert.equal(levelLabelKey('xhigh'), 'model.reasoning.levels.xhigh')
  assert.equal(levelDescriptionKey('auto'), 'model.reasoning.levelDescriptions.auto')
})

test('levelEnablesThinking treats everything but off as enabled', () => {
  assert.equal(levelEnablesThinking('off'), false)
  assert.equal(levelEnablesThinking(''), false)
  assert.equal(levelEnablesThinking(undefined), false)
  assert.equal(levelEnablesThinking('auto'), true)
  assert.equal(levelEnablesThinking('max'), true)
})

test('optionsFor offers the generic ladder without capabilities', () => {
  assert.deepEqual(optionsFor(undefined), FALLBACK_REASONING_LEVELS)
  assert.deepEqual(optionsFor(null), FALLBACK_REASONING_LEVELS)
  assert.deepEqual(optionsFor({}), FALLBACK_REASONING_LEVELS)
  assert.notEqual(optionsFor(undefined), FALLBACK_REASONING_LEVELS, 'callers get a fresh array')
})

test('optionsFor follows thinking_levels exactly, in ladder order', () => {
  assert.deepEqual(optionsFor({ thinking_levels: ['high', 'low', 'medium'] }), ['low', 'medium', 'high'])
  assert.deepEqual(optionsFor({ thinking_levels: ['auto', 'off'] }), ['off', 'auto'])
  assert.deepEqual(optionsFor({ thinking_levels: ['max', 'weird', 'xhigh'] }), ['xhigh', 'max'])
})

test('optionsFor never offers off for an always-on reasoning model', () => {
  // deepseek-reasoner / qwq-plus / gemini-3: the catalog maps `off` to null,
  // so the backend cannot switch thinking off for them.
  const alwaysOn = { thinking_levels: ['auto', 'minimal', 'low', 'medium', 'high'] }
  assert.equal(optionsFor(alwaysOn).includes('off'), false)
  assert.deepEqual(optionsFor(alwaysOn), ['auto', 'minimal', 'low', 'medium', 'high'])
})

test('optionsFor shows a lone off for a model that cannot think', () => {
  assert.deepEqual(optionsFor({ thinking_levels: [] }), ['off'])
  assert.deepEqual(optionsFor({ thinking_levels: ['nonsense'] }), ['off'])
})

test('modelCannotDisableThinking only fires for models with levels but no off', () => {
  assert.equal(modelCannotDisableThinking({ thinking_levels: ['auto', 'high'] }), true)
  assert.equal(modelCannotDisableThinking({ thinking_levels: ['off', 'auto'] }), false)
  assert.equal(modelCannotDisableThinking({ thinking_levels: [] }), false)
  assert.equal(modelCannotDisableThinking({}), false, 'no capabilities → nothing to claim')
  assert.equal(modelCannotDisableThinking(undefined), false)
  assert.equal(modelCannotDisableThinking(null), false)
})

test('clampLevel rescues a stored off when the model cannot be switched off', () => {
  const alwaysOn = optionsFor({ thinking_levels: ['auto', 'low', 'high'] })
  assert.equal(clampLevel('off', alwaysOn), 'auto')
  assert.equal(clampLevel('medium', alwaysOn), 'auto')
  assert.equal(clampLevel('high', alwaysOn), 'high')
  // …and a stored graded level collapses to off for a model that cannot think.
  assert.equal(clampLevel('high', optionsFor({ thinking_levels: [] })), 'off')
})

test('supportedLevels and modelCanThink reflect thinking_levels exactly', () => {
  assert.deepEqual(supportedLevels({ thinking_levels: ['medium', 'off', 'low'] }), ['off', 'low', 'medium'])
  assert.deepEqual(supportedLevels({ thinking_levels: [] }), [])
  assert.deepEqual(supportedLevels(undefined), [])
  assert.equal(modelCanThink({ thinking_levels: ['auto'] }), true)
  assert.equal(modelCanThink({ thinking_levels: [] }), false)
  assert.equal(modelCanThink(undefined), false)
})

test('canonicalLevels normalizes case, drops duplicates and unknown values', () => {
  assert.deepEqual(canonicalLevels(['HIGH', 'high', 'nope', ' low ']), ['low', 'high'])
  assert.deepEqual(canonicalLevels(null), [])
})

test('clampLevel keeps supported values and degrades gracefully', () => {
  const options = ['off', 'auto', 'low', 'high'] as const
  assert.equal(clampLevel('high', options), 'high')
  assert.equal(clampLevel('medium', options), 'auto', 'unsupported graded level falls back to auto')
  assert.equal(clampLevel('', options), 'off')
  assert.equal(clampLevel(undefined, options), 'off')
  assert.equal(clampLevel('medium', ['low', 'high']), 'low', 'no off/auto → first supported')
  assert.equal(clampLevel('medium', []), 'off')
})
