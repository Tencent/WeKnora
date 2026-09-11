import assert from 'node:assert/strict'
import test from 'node:test'
import { assertSupportedTrainingProjection } from './trainingOrchestrationContract'

test('accepts the supported training orchestration projection version', () => {
  assert.doesNotThrow(() => assertSupportedTrainingProjection({ schema_version: 'training-orchestration/v1' }))
})

test('rejects missing and unsupported training orchestration projection versions', () => {
  assert.throws(() => assertSupportedTrainingProjection({}), /缺少版本信息/)
  assert.throws(
    () => assertSupportedTrainingProjection({ schema_version: 'training-orchestration/v2' }),
    /版本暂不受支持/,
  )
})
