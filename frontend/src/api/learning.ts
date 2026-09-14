import { get, post, put, del } from '@/utils/request'

export type LearningSettings = { enabled: boolean; algorithm_version: string }
export type LearningMasteryView = {
  state: 'unseen' | 'learning' | 'mastered' | 'review_due'
  p_mastery: number
  attempts: number
  correct: number
  consecutive_correct: number
  last_assessed_at?: string
  next_review_at?: string
  source_stale: boolean
}
export type LearningNodeView = {
  page_id: string
  knowledge_base_id: string
  slug: string
  title: string
  page_type: string
  summary: string
  familiar: boolean
  mastery: LearningMasteryView
}
export type LearningOverview = {
  enabled: boolean
  algorithm_version: string
  knowledge_base_id: string
  total_nodes: number
  counts: Record<LearningMasteryView['state'], number>
}
export type LearningRecommendation = LearningNodeView & {
  score: number
  components: { review_need: number; graph_frontier: number; interest_match: number; content_quality: number }
  reason_codes: string[]
}
export type LearningAnswerResult = {
  attempt_id: string
  question_id: string
  selected_option: string
  correct: boolean
  correct_option: string
  explanation: string
  evidence: { chunk_id: string; knowledge_id: string; quote: string }[]
  mastery: LearningMasteryView
}
export type LearningQuestionView = {
  id: string
  prompt: string
  options: { id: string; text: string }[]
} & ({ answered: false; result?: never } | { answered: true; result?: LearningAnswerResult })
export type LearningQuizView = {
  id: string
  page_id: string
  knowledge_base_id: string
  slug: string
  title: string
  status: 'pending' | 'running' | 'ready' | 'failed' | 'stale'
  error_code?: string
  questions: LearningQuestionView[]
  algorithm_version: string
}
export type LearningClearResult = { deleted_attempts: number; deleted_mastery: number; deleted_quizzes: number }
// Export remains an opaque server document, downloaded without reshaping.
export type LearningExport = Record<string, unknown>
type Envelope<T> = { success: true; data: T } | { success: false; error?: { code?: string; message?: string } }

async function data<T>(request: Promise<Envelope<T>>): Promise<T> {
  const response = await request
  if (response.success !== true) throw response
  return response.data
}

const root = '/api/v1/learning'
const id = encodeURIComponent
const scope = (kb?: string) => kb ? `?${new URLSearchParams({ knowledge_base_id: kb })}` : ''
export const learningApi = {
  settings: (signal?: AbortSignal) => data(get<Envelope<LearningSettings>>(`${root}/settings`, { signal })),
  setEnabled: (enabled: boolean, signal?: AbortSignal) =>
    data(put<Envelope<LearningSettings>>(`${root}/settings`, { enabled }, { signal })),
  overview: (kb: string, signal?: AbortSignal) =>
    data(get<Envelope<LearningOverview>>(`${root}/overview${scope(kb)}`, { signal })),
  recommendations: (kb: string, signal?: AbortSignal) =>
    data(get<Envelope<LearningRecommendation[]>>(`${root}/recommendations${scope(kb)}&limit=5`, { signal })),
  node: (page: string, signal?: AbortSignal) =>
    data(get<Envelope<LearningNodeView>>(`${root}/nodes/${id(page)}`, { signal })),
  recordView: (page: string, signal?: AbortSignal) =>
    data(post<Envelope<null>>(`${root}/nodes/${id(page)}/view`, {}, { signal })),
  overlay: (kb: string, slugs: string[], signal?: AbortSignal) =>
    data(post<Envelope<LearningNodeView[]>>(`${root}/overlay`, { knowledge_base_id: kb, slugs }, { signal })),
  prepareQuiz: (page: string, signal?: AbortSignal) =>
    data(post<Envelope<LearningQuizView>>(`${root}/question-sets`, { page_id: page }, { signal })),
  quiz: (quiz: string, signal?: AbortSignal) =>
    data(get<Envelope<LearningQuizView>>(`${root}/question-sets/${id(quiz)}`, { signal })),
  answer: (body: { question_id: string; option_id: string; attempt_id: string }, signal?: AbortSignal) =>
    data(post<Envelope<LearningAnswerResult>>(`${root}/attempts`, body, { signal })),
  export: (kb?: string, signal?: AbortSignal) =>
    data(get<Envelope<LearningExport>>(`${root}/export${scope(kb)}`, { signal })),
  clear: (kb?: string) => data(del<Envelope<LearningClearResult>>(`${root}/profile${scope(kb)}`)),
}
export type LearningApi = typeof learningApi
