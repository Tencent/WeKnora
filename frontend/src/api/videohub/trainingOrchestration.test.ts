import assert from 'node:assert/strict'
import test from 'node:test'
import { trainingJobErrorMessage, trainingJobWarningMessage } from './trainingOrchestrationErrors'

function failedJob(errorCode?: string) {
	return { error_code: errorCode }
}

test('maps stable training failure codes to understandable messages', () => {
  assert.match(trainingJobErrorMessage(failedJob('evidence_retrieval_failed')), /内容暂时无法召回/)
  assert.match(trainingJobErrorMessage(failedJob('source_changed')), /视频内容发生变化/)
  assert.match(trainingJobErrorMessage(failedJob('model_output_invalid')), /格式异常/)
})

test('does not expose unknown backend errors to users', () => {
  assert.equal(trainingJobErrorMessage(failedJob('provider_internal_failure')), '学习路径生成失败，当前结果未受影响，请重试。')
})

test('maps retrieval degradation to a completion warning', () => {
  assert.match(trainingJobWarningMessage({ warning_code: 'evidence_retrieval_degraded' }), /已使用规划证据完成生成/)
  assert.equal(trainingJobWarningMessage({ warning_code: 'unknown_warning' }), '')
})
