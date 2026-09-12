import assert from 'node:assert/strict'
import test from 'node:test'
import { DATASET_TEMPLATE, DatasetValidationError, readDatasetFile, utf8Size, validateDatasetContent, validateImportEnvelope } from './datasetValidation'
import type { DatasetLimits } from '../../api/evaluation/datasets'

export const limits: DatasetLimits = { max_request_body_bytes: 65536, max_passages: 10, max_questions: 10, max_relevance: 100, max_passage_bytes: 1024, max_question_bytes: 256, max_name_chars: 255, max_id_chars: 128, max_grade: 2147483647 }
const copy = () => structuredClone(DATASET_TEMPLATE)
const code = (expected: string) => (error: unknown) => error instanceof DatasetValidationError && error.code === expected
test('oversized files are rejected before reading any file content', async () => {
  let reads = 0
  await assert.rejects(readDatasetFile({ size: 65537, name: 'large.json', text: async () => { reads++; return '{}' } }, limits), code('fileSize'))
  assert.equal(reads, 0)
})
test('bad JSON and unlabelled documents have actionable distinct errors', async () => {
  await assert.rejects(readDatasetFile({ size: 1, name: 'data.json', text: async () => '{' }, limits), code('json'))
  await assert.rejects(readDatasetFile({ size: 1, name: 'paper.pdf', text: async () => 'unused' }, limits), code('document'))
  assert.throws(() => validateDatasetContent({ data: [] }, limits), code('shape'))
})
test('grade zero and unanswerable questions survive file import unchanged', async () => {
  const content = copy(); content.relevance[0]!.grade = 0; content.questions[0]!.answer = ''
  const encoded = JSON.stringify(content)
  const result = await readDatasetFile({ size: utf8Size(encoded), name: 'registry-input.json', text: async () => encoded }, limits)
  assert.equal(result.relevance[0]!.grade, 0)
  assert.equal(result.questions[0]!.answer, '')
  content.relevance = []
  assert.deepEqual(validateDatasetContent(content, limits).relevance, [])
})
test('dangling references, duplicate IDs and duplicate relevance pairs are rejected', () => {
  const dangling = copy(); dangling.relevance[0]!.pid = 'missing'
  assert.throws(() => validateDatasetContent(dangling, limits), code('reference'))
  const passages = copy(); passages.passages.push({ ...passages.passages[0]! })
  assert.throws(() => validateDatasetContent(passages, limits), code('duplicate'))
  const questions = copy(); questions.questions.push({ ...questions.questions[0]! })
  assert.throws(() => validateDatasetContent(questions, limits), code('duplicate'))
  const pairs = copy(); pairs.relevance.push({ ...pairs.relevance[0]! })
  assert.throws(() => validateDatasetContent(pairs, limits), code('duplicate'))
})
test('missing, string, negative, fractional and overflowing grades are never coerced', () => {
  for (const grade of [undefined, null, '0', -1, 0.5, 2147483648]) {
    const value = copy() as unknown as { relevance: Array<{ grade: unknown }> }
    value.relevance[0]!.grade = grade
    assert.throws(() => validateDatasetContent(value, limits), code('grade'))
  }
})
test('live service row and UTF-8 limits are enforced, including envelope overhead', () => {
  assert.throws(() => validateDatasetContent(copy(), { ...limits, max_questions: 0 }), code('count'))
  const content = copy(); content.passages[0]!.content = '中文'
  assert.throws(() => validateDatasetContent(content, { ...limits, max_passage_bytes: 5 }), code('length'))
  const request = { content, name: '数据集', description: '', request_id: 'test' }
  assert.throws(() => validateImportEnvelope(request, { ...limits, max_request_body_bytes: utf8Size(JSON.stringify(content)) }), code('fileSize'))
  assert.throws(() => validateImportEnvelope({ ...request, name: '' }, limits), code('text'))
})
