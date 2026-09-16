import type { DatasetContent, DatasetLimits } from '@/api/evaluation/datasets'

export class DatasetValidationError extends Error {
  constructor(public readonly code: string, public readonly path = '', public readonly limit?: number) {
    super(code)
  }
}
const fail = (code: string, path = '', limit?: number): never => { throw new DatasetValidationError(code, path, limit) }
export const utf8Size = (value: string): number => new TextEncoder().encode(value).byteLength
const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v)
function fields(v: Record<string, unknown>, allowed: string[], path: string) {
  for (const key of Object.keys(v)) if (!allowed.includes(key)) fail('shape', `${path}.${key}`)
}
function string(v: unknown, path: string, max: number, bytes = true, allowEmpty = false): asserts v is string {
  if (typeof v !== 'string' || (!allowEmpty && !v.trim())) fail('text', path)
  if ((bytes ? utf8Size(v as string) : Array.from(v as string).length) > max) fail('length', path, max)
}
export function validateDatasetContent(value: unknown, limits: DatasetLimits): DatasetContent {
  if (!object(value)) fail('shape')
  const input = value as Record<string, unknown>
  fields(input, ['passages', 'questions', 'relevance'], '')
  for (const [key, max] of [['passages', limits.max_passages], ['questions', limits.max_questions], ['relevance', limits.max_relevance]] as const) {
    if (!Array.isArray(input[key])) fail('shape', key)
    if ((input[key] as unknown[]).length > max) fail('count', key, max)
    if (key !== 'relevance' && !(input[key] as unknown[]).length) fail('empty', key)
  }
  const passages = new Set<string>(), questions = new Set<string>(), edges = new Set<string>()
  for (const [index, row] of (input.passages as unknown[]).entries()) {
    const path = `passages[${index}]`
    if (!object(row)) fail('shape', path)
    const p = row as Record<string, unknown>
    fields(p, ['pid', 'content', 'metadata'], path)
    string(p.pid, `${path}.pid`, limits.max_id_chars, false)
    string(p.content, `${path}.content`, limits.max_passage_bytes)
    if (p.pid !== p.pid.trim()) fail('idWhitespace', `${path}.pid`)
    if (passages.has(p.pid)) fail('duplicate', `${path}.pid`)
    passages.add(p.pid)
    if (p.metadata !== undefined && (!object(p.metadata) || utf8Size(JSON.stringify(p.metadata)) > limits.max_passage_bytes)) fail('metadata', path)
  }
  for (const [index, row] of (input.questions as unknown[]).entries()) {
    const path = `questions[${index}]`
    if (!object(row)) fail('shape', path)
    const q = row as Record<string, unknown>
    fields(q, ['qid', 'question', 'answer'], path)
    string(q.qid, `${path}.qid`, limits.max_id_chars, false)
    string(q.question, `${path}.question`, limits.max_question_bytes)
    string(q.answer, `${path}.answer`, limits.max_question_bytes, true, true)
    if (q.qid !== q.qid.trim()) fail('idWhitespace', `${path}.qid`)
    if (questions.has(q.qid)) fail('duplicate', `${path}.qid`)
    questions.add(q.qid)
  }
  for (const [index, row] of (input.relevance as unknown[]).entries()) {
    const path = `relevance[${index}]`
    if (!object(row)) fail('shape', path)
    const r = row as Record<string, unknown>
    fields(r, ['qid', 'pid', 'grade'], path)
    if (typeof r.qid !== 'string' || typeof r.pid !== 'string' || !questions.has(r.qid) || !passages.has(r.pid)) fail('reference', path)
    if (typeof r.grade !== 'number' || !Number.isInteger(r.grade) || r.grade < 0 || r.grade > limits.max_grade) fail('grade', path, limits.max_grade)
    const key = JSON.stringify([r.qid, r.pid])
    if (edges.has(key)) fail('duplicate', path)
    edges.add(key)
  }
  return value as DatasetContent
}
export async function readDatasetFile(file: Pick<File, 'size' | 'name' | 'text'>, limits: DatasetLimits): Promise<DatasetContent> {
  if (file.size > limits.max_request_body_bytes) fail('fileSize', '', limits.max_request_body_bytes)
  if (!file.name.toLowerCase().endsWith('.json')) fail('document')
  let value: unknown
  try { value = JSON.parse(await file.text()) } catch { fail('json') }
  return validateDatasetContent(value, limits)
}
export function validateImportEnvelope(value: { name: string; description: string; content: DatasetContent; request_id: string }, limits: DatasetLimits) {
  string(value.name, 'name', limits.max_name_chars, false)
  string(value.description, 'description', limits.max_question_bytes, true, true)
  if (utf8Size(JSON.stringify(value)) > limits.max_request_body_bytes) fail('fileSize', '', limits.max_request_body_bytes)
}
export const DATASET_TEMPLATE: DatasetContent = {
  passages: [{ pid: 'p-1', content: 'WeKnora is an open-source knowledge platform.', metadata: { source: 'template' } }],
  questions: [{ qid: 'q-1', question: 'What is WeKnora?', answer: 'An open-source knowledge platform.' }],
  relevance: [{ qid: 'q-1', pid: 'p-1', grade: 1 }],
}
