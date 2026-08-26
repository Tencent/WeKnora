import assert from 'node:assert/strict'
import test from 'node:test'

import {
  XQUIK_DEFAULT_RESULTS_PER_QUERY,
  XQUIK_MAX_QUERIES,
  XQUIK_MAX_QUERY_LENGTH,
  XQUIK_MAX_RESULTS_PER_QUERY,
  validateXquikSettings,
  xquikQueries,
  xquikSettingsSignature,
  xquikValidationCredentials,
} from './xquikConfig.ts'

test('xquikQueries trims, deduplicates, and preserves query order', () => {
  assert.deepEqual(xquikQueries(' from:weknora\r\nrag lang:en\nfrom:weknora\n'), [
    'from:weknora',
    'rag lang:en',
  ])
})

test('validateXquikSettings accepts valid settings', () => {
  assert.equal(validateXquikSettings({
    queries: 'xquik api',
    results_per_query: XQUIK_MAX_RESULTS_PER_QUERY,
  }), null)
})

test('validateXquikSettings rejects missing and oversized query sets', () => {
  assert.equal(validateXquikSettings({ queries: ' ' }), 'queriesRequired')
  assert.equal(validateXquikSettings({
    queries: Array.from({ length: XQUIK_MAX_QUERIES + 1 }, (_, index) => `query-${index}`).join('\n'),
  }), 'tooManyQueries')
  assert.equal(validateXquikSettings({
    queries: '界'.repeat(XQUIK_MAX_QUERY_LENGTH + 1),
  }), 'queryTooLong')
})

test('validateXquikSettings requires a bounded integer result count', () => {
  assert.equal(validateXquikSettings({ queries: 'xquik', results_per_query: 0 }), 'resultsOutOfRange')
  assert.equal(validateXquikSettings({ queries: 'xquik', results_per_query: 1.5 }), 'resultsOutOfRange')
  assert.equal(validateXquikSettings({
    queries: 'xquik',
    results_per_query: XQUIK_MAX_RESULTS_PER_QUERY + 1,
  }), 'resultsOutOfRange')
})

test('xquikValidationCredentials adds only validation-time settings', () => {
  assert.deepEqual(
    xquikValidationCredentials({ api_key: 'secret' }, { queries: 'xquik' }),
    { api_key: 'secret', queries: 'xquik', results_per_query: XQUIK_DEFAULT_RESULTS_PER_QUERY },
  )
})

test('xquikSettingsSignature tracks semantic setting changes', () => {
  const baseline = xquikSettingsSignature({ queries: 'xquik\nrag', results_per_query: 100 })
  assert.equal(
    xquikSettingsSignature({ queries: ' xquik \r\nrag\nxquik', results_per_query: '100' }),
    baseline,
  )
  assert.notEqual(xquikSettingsSignature({ queries: 'xquik', results_per_query: 100 }), baseline)
  assert.notEqual(xquikSettingsSignature({ queries: 'xquik\nrag', results_per_query: 200 }), baseline)
})
