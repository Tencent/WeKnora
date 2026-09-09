import test from 'node:test'
import assert from 'node:assert/strict'

import {
  assertSupportedKnowledgeContract,
  resolveProcessingErrorMessage,
  resolveProcessingFailureMessage,
  SUPPORTED_KNOWLEDGE_CONTRACT_VERSION,
} from './videoProcessingStatus.ts'

test('frontend explicitly accepts the backend knowledge contract version', () => {
  assert.doesNotThrow(() => assertSupportedKnowledgeContract(SUPPORTED_KNOWLEDGE_CONTRACT_VERSION))
  assert.throws(() => assertSupportedKnowledgeContract('p3-wiki-object/v2'), /契约版本不兼容/)
})

test('summary generation failures use stable business messages', () => {
  assert.match(resolveProcessingErrorMessage('llm_connection_closed'), /连接提前关闭/)
  assert.match(resolveProcessingErrorMessage('llm_stream_incomplete'), /内容不完整/)
  assert.match(resolveProcessingErrorMessage('llm_deadline_exceeded'), /生成超时/)
  assert.match(resolveProcessingErrorMessage('llm_stream_idle_timeout'), /生成超时/)
  assert.match(resolveProcessingErrorMessage('summary_contract_invalid'), /契约/)
})

test('knowledge contract failure is not presented as an untriggered task', () => {
  assert.equal(resolveProcessingFailureMessage({
    job_id: 'graph-1',
    job_type: 'graph',
    category: 'wiki_artifact',
    code: 'content_contract_failed',
    message: 'P3 knowledge object validation failed: core content is required',
    updated_at: '2026-09-08T00:24:25+08:00',
  }), 'AI 契约校验失败：输出未通过知识契约，未写入知识图谱。请重试知识提取')
})

test('other failures preserve the backend message', () => {
  assert.equal(resolveProcessingFailureMessage({
    job_id: 'summary-1',
    job_type: 'summary',
    category: 'response_parse',
    code: 'response_parse',
    message: '总结格式错误',
    updated_at: '2026-09-08T00:24:25+08:00',
  }), '总结格式错误')
})
