import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { effectScope, reactive } from 'vue'
import type { LearningApi, LearningAnswerResult, LearningNodeView, LearningOverview, LearningQuizView, LearningSettings } from '../api/learning'
import { useLearningState, type LearningContext } from './useLearningState.ts'
import { learningAttemptId, learningQuizPointers, learningScopeKey, publicLearningQuiz, learningToolReference, POLL_LIMIT, pollDelay, waitForPoll } from './learningHelpers.ts'

const mastery = { state: 'learning' as const, p_mastery: 0.6, attempts: 1, correct: 1, consecutive_correct: 1, source_stale: false }
const node = (page = 'page', kb = 'kb'): LearningNodeView => ({ page_id: page, knowledge_base_id: kb, slug: `concept/${page}`, title: page, page_type: 'concept', summary: '', familiar: true, mastery })
const quiz = (status: LearningQuizView['status'] = 'ready', page = 'page', kb = 'kb'): LearningQuizView => ({
  id: 'quiz', page_id: page, knowledge_base_id: kb, slug: `concept/${page}`, title: page, status, algorithm_version: 'bkt-v1',
  questions: [{ id: 'question', prompt: 'Which option?', options: ['a', 'b', 'c', 'd'].map(id => ({ id, text: id })), answered: false }],
})
const result: LearningAnswerResult = { attempt_id: 'attempt', question_id: 'question', selected_option: 'a', correct: true,
  correct_option: 'a', explanation: 'server explanation', evidence: [{ chunk_id: 'chunk', knowledge_id: 'source', quote: 'exact source quote' }], mastery }
const summary = (kb = 'kb'): LearningOverview => ({ enabled: true, algorithm_version: 'bkt-v1', knowledge_base_id: kb,
  total_nodes: 4, counts: { unseen: 1, learning: 1, mastered: 1, review_due: 1 } })
const tick = () => new Promise<void>(resolve => setImmediate(resolve))
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const disposers: (() => void)[] = []
afterEach(() => { for (const dispose of disposers.splice(0)) dispose() })
function setup(overrides: Partial<LearningApi> = {}, options: Parameters<typeof useLearningState>[2] = {}) {
  const context = reactive<LearningContext>({ scope: { tenantId: 'tenant', userId: 'user', kbId: 'kb', available: true }, pageId: 'page', slugs: ['concept/page'] })
  let enabled = true
  const calls: { name: string; args: unknown[] }[] = []
  const api: LearningApi = {
    settings: async () => ({ enabled, algorithm_version: 'bkt-v1' }),
    setEnabled: async value => ({ enabled: enabled = value, algorithm_version: 'bkt-v1' }),
    overview: async kb => summary(kb), recommendations: async () => [], node: async page => node(page, context.scope.kbId),
    recordView: async () => null, overlay: async (kb, slugs) => slugs.map(slug => ({ ...node(slug, kb), slug })),
    prepareQuiz: async page => quiz('ready', page, context.scope.kbId), quiz: async () => quiz(), answer: async () => result,
    export: async () => ({ settings: { enabled } }), clear: async () => ({ deleted_attempts: 1, deleted_mastery: 1, deleted_quizzes: 1 }),
    ...overrides,
  }
  for (const name of Object.keys(api) as (keyof LearningApi)[]) {
    const original = api[name] as (...args: unknown[]) => unknown
    ;(api as any)[name] = (...args: unknown[]) => { calls.push({ name, args }); return original(...args) }
  }
  const scope = effectScope()
  const controller = scope.run(() => useLearningState(() => context, api, { attemptId: () => 'attempt', ...options }))!
  disposers.push(() => scope.stop())
  return { controller, context, calls, stop: () => scope.stop() }
}

test('opt-in is explicit; privacy export and clear remain available while off', async () => {
  const { controller, calls } = setup({ settings: async () => ({ enabled: false, algorithm_version: 'v1' }) })
  await tick()
  assert.deepEqual(calls.map(call => call.name), ['settings'])
  assert.ok(await controller.exportData())
  await controller.privacy('clearKB')
  assert.equal(calls.find(call => call.name === 'clear')?.args[0], 'kb')
  assert.equal(controller.state.quiz, null)
  await controller.privacy('enable'); await tick()
  assert.ok(calls.some(call => call.name === 'overview'))
})

test('clear waits for fresh consent and keeps deletion counts without restoring enabled state', async () => {
  let cleared = false
  const settings = deferred<{ enabled: boolean; algorithm_version: string }>()
  const { controller } = setup({ settings: () => cleared ? settings.promise : Promise.resolve({ enabled: true, algorithm_version: 'v1' }),
    clear: async () => { cleared = true; return { deleted_attempts: 3, deleted_mastery: 1, deleted_quizzes: 1 } } })
  await tick()
  const deletion = controller.privacy('clearAll')
  await tick()
  assert.equal(controller.state.settings, null)
  assert.equal(controller.state.busy, true)
  assert.equal(controller.state.clearResult?.deleted_attempts, 3)
  settings.resolve({ enabled: false, algorithm_version: 'v1' })
  await deletion
  assert.equal((controller.state.settings as LearningSettings | null)?.enabled, false)
  assert.equal(controller.state.clearResult?.deleted_attempts, 3)
})

test('new practice retains completed feedback but all scope resets remove it', async () => {
  let generation = 0
  const answered = quiz(); answered.questions[0] = { ...answered.questions[0], answered: true, result }
  const { controller, context } = setup({ prepareQuiz: async () => ++generation === 1 ? answered : { ...quiz(), id: 'next' } })
  await tick(); await controller.loadQuiz('', true)
  await controller.loadQuiz('', true)
  assert.equal(controller.state.quiz?.id, 'next')
  assert.equal(controller.state.previousQuiz?.questions[0].result?.explanation, 'server explanation')
  context.pageId = 'other'
  assert.equal(controller.state.previousQuiz, null)
})

test('window focus refresh preserves stable consent and pending submissions', async () => {
  const pending = deferred<LearningAnswerResult>()
  const { controller } = setup({ answer: () => pending.promise })
  await tick(); await controller.loadQuiz('', true)
  const settings = controller.state.settings
  const epoch = controller.state.epoch
  const submission = controller.submit('question', 'a')
  await tick(); await controller.refreshConsent(); await tick()
  assert.equal(controller.state.settings, settings)
  assert.equal(controller.state.epoch, epoch)
  assert.equal(controller.state.submitting, 'question')
  pending.resolve(result); await submission
  assert.equal(controller.state.quiz?.questions[0].answered, true)
})

test('window focus clears personal UI when the server revoked consent', async () => {
  let enabled = true
  const { controller } = setup({ settings: async () => ({ enabled, algorithm_version: 'bkt-v1' }) })
  await tick(); await controller.loadQuiz('', true)
  enabled = false
  await controller.refreshConsent(); await tick()
  assert.equal(controller.state.settings?.enabled, false)
  assert.equal(controller.state.quiz, null)
  assert.equal(controller.state.node, null)
  assert.deepEqual(controller.state.recommendations, [])
})

test('questions have no pre-answer key or explanation even if malformed data includes them', () => {
  const input = quiz() as any
  input.correct_option = 'SECRET'
  input.questions[0].correct_option = 'SECRET'
  input.questions[0].result = { ...result, explanation: 'SECRET' }
  input.questions[0].options[0].correct = true
  const output = publicLearningQuiz(input)
  assert.doesNotMatch(JSON.stringify(output), /SECRET|correct_option|explanation|"correct"/)
  assert.equal(publicLearningQuiz({ ...input, status: 'pending' }).questions.length, 0)
})

test('only submitted answers expose server feedback; retries reuse the same attempt and option', async () => {
  let count = 0
  const pending = deferred<LearningAnswerResult>()
  const { controller, calls } = setup({ answer: () => ++count === 1 ? Promise.reject(new Error('network')) : pending.promise })
  await tick(); await controller.loadQuiz('', true)
  assert.equal(controller.state.quiz?.questions[0].result, undefined)
  await controller.submit('question', 'a')
  assert.equal(controller.state.quiz?.questions[0].result, undefined)
  await controller.submit('question', 'b')
  assert.equal(count, 1)
  const submission = controller.submit('question', 'a')
  assert.equal(controller.state.quiz?.questions[0].answered, false)
  pending.resolve(result); await submission
  assert.equal((controller.state.quiz as LearningQuizView | null)?.questions[0].result?.explanation, 'server explanation')
  const answers = calls.filter(call => call.name === 'answer')
  assert.deepEqual(answers[0].args[0], answers[1].args[0])
  await controller.submit('question', 'a')
  assert.equal(count, 2)
})

test('a page read started before an answer cannot overwrite the assessed mastery badge', async () => {
  const pending = deferred<LearningNodeView>()
  let reads = 0
  const { controller } = setup({ node: () => ++reads === 1 ? pending.promise : Promise.resolve(node()) })
  await tick(); await controller.loadQuiz('', true)
  await controller.submit('question', 'a')
  await tick()
  pending.resolve({ ...node(), mastery: { ...mastery, state: 'unseen', attempts: 0, p_mastery: 0.2 } })
  await tick()
  assert.equal(controller.state.node?.mastery.attempts, 1)
  assert.equal(controller.state.node?.mastery.p_mastery, 0.6)
})

test('pending/running polling is bounded, backed off, cancellable and resumable', async () => {
  const delays: number[] = []
  const { controller, calls } = setup({ prepareQuiz: async () => quiz('pending'), quiz: async () => quiz('running') },
    { wait: async delay => { delays.push(delay) } })
  await tick(); await controller.loadQuiz('', true)
  assert.equal(calls.filter(call => call.name === 'quiz').length, POLL_LIMIT)
  assert.deepEqual(delays.slice(0, 5), [1000, 2000, 4000, 8000, 10000])
  assert.equal(controller.state.paused, true)
  assert.equal(controller.state.quizLoading, false)
  assert.equal(controller.state.quiz?.questions.length, 0)
  const wait = deferred<void>()
  const second = setup({ prepareQuiz: async () => quiz('pending') }, { wait: () => wait.promise })
  await tick()
  const preparation = second.controller.loadQuiz('', true)
  await tick(); second.controller.cancelQuiz(); wait.resolve(); await preparation
  assert.equal(second.calls.filter(call => call.name === 'quiz').length, 0)
  await second.controller.loadQuiz('quiz')
  assert.equal(second.controller.state.quiz?.status, 'ready')
})

test('page, tenant, user and consent changes invalidate late quiz and answer responses', async () => {
  for (const change of ['page', 'tenant', 'user', 'kb', 'consent'] as const) {
    const pending = deferred<LearningAnswerResult>()
    const { controller, context } = setup({ answer: () => pending.promise })
    await tick(); await controller.loadQuiz('', true)
    const submission = controller.submit('question', 'a')
    if (change === 'page') context.pageId = 'other'
    if (change === 'tenant') context.scope.tenantId = 'other'
    if (change === 'user') context.scope.userId = 'other'
    if (change === 'kb') context.scope.kbId = 'other'
    if (change === 'consent') await controller.privacy('disable')
    assert.equal(controller.state.quiz, null)
    pending.resolve(result); await submission
    assert.equal(controller.state.quiz, null)
    assert.equal(controller.state.submitting, '')
  }
})

test('a late quiz response cannot restore state after opt-out or unmount', async () => {
  for (const unmount of [false, true]) {
    const pending = deferred<LearningQuizView>()
    const { controller, stop } = setup({ prepareQuiz: () => pending.promise })
    await tick()
    const preparation = controller.loadQuiz('', true)
    if (unmount) stop()
    else await controller.privacy('disable')
    pending.resolve(quiz()); await preparation
    assert.equal(controller.state.quiz, null)
  }
})

test('late settings from a departed and revisited tenant cannot reactivate consent', async () => {
  const pending = deferred<{ enabled: boolean; algorithm_version: string }>()
  let count = 0
  const { controller, context } = setup({ settings: () => ++count === 1 ? pending.promise : Promise.resolve({ enabled: false, algorithm_version: 'v1' }) })
  context.scope.tenantId = 'other'; context.scope.tenantId = 'tenant'
  await tick(); pending.resolve({ enabled: true, algorithm_version: 'old' }); await tick()
  assert.equal(controller.state.settings?.enabled, false)
})

test('stale quiz and source failures have no answerable questions and can regenerate', async () => {
  let stale = true
  const { controller } = setup({ answer: async () => { throw { status: 409, error: { code: 'learning_stale' } } },
    prepareQuiz: async () => quiz(stale ? 'stale' : 'ready') })
  await tick(); await controller.loadQuiz('', true)
  assert.equal(controller.state.quiz?.questions.length, 0)
  stale = false; await controller.loadQuiz('', true)
  await controller.submit('question', 'a')
  assert.equal(controller.state.quiz?.status, 'stale')
  assert.equal(controller.state.quiz?.questions.length, 0)
})

test('overlay batches at 2000, filters scope and ignores old graph responses', async () => {
  const old = deferred<LearningNodeView[]>()
  let count = 0
  const { controller, context, calls } = setup({ recordView: async () => null,
    overlay: async (kb, slugs) => ++count === 1 ? old.promise : slugs.map(slug => ({ ...node(slug, kb), slug })) })
  await tick()
  context.slugs = Array.from({ length: 2001 }, (_, i) => `node/${i}`)
  await tick(); old.resolve([node('wrong')]); await tick()
  assert.equal(controller.state.overlay.length, 2001)
  assert.ok(controller.state.overlay.every(item => item.slug.startsWith('node/')))
  assert.ok(calls.filter(call => call.name === 'overlay').every(call => (call.args[1] as string[]).length <= 2000))
})

test('scope mismatch in quiz or overview never renders foreign personal data', async () => {
  const { controller } = setup({ quiz: async () => quiz('ready', 'page', 'foreign'), overview: async () => summary('foreign') })
  await tick(); await controller.loadQuiz('quiz')
  assert.equal(controller.state.quiz, null)
  assert.equal(controller.state.overview, null)
})

test('cross-panel privacy invalidation clears state immediately and blocks reads until the mutation ends', async () => {
  const mutation = deferred<{ deleted_attempts: number; deleted_mastery: number; deleted_quizzes: number }>()
  const first = setup({ clear: () => mutation.promise })
  const second = setup()
  await tick(); await second.controller.loadQuiz('', true)
  const deletion = first.controller.privacy('clearAll')
  assert.equal(second.controller.state.quiz, null)
  assert.equal(second.controller.state.busy, true)
  const calls = second.calls.length
  await second.controller.loadQuiz('quiz')
  assert.equal(second.calls.length, calls)
  mutation.resolve({ deleted_attempts: 1, deleted_mastery: 1, deleted_quizzes: 1 })
  await deletion; await tick()
  assert.equal(second.controller.state.busy, false)
})

test('privacy operations block newly mounted cards and KB switches until completion', async () => {
  const mutation = deferred<{ deleted_attempts: number; deleted_mastery: number; deleted_quizzes: number }>()
  const first = setup({ clear: () => mutation.promise })
  const second = setup()
  await tick()
  const deletion = first.controller.privacy('clearKB')
  const newcomer = setup()
  second.context.scope.kbId = 'other'
  first.context.scope.kbId = 'other'
  const reads = second.calls.length
  try {
    await tick()
    assert.equal(newcomer.controller.state.busy, true)
    assert.equal(newcomer.calls.length, 0)
    assert.equal(second.controller.state.busy, true)
    assert.equal(second.calls.length, reads)
    assert.equal(first.controller.state.busy, true)
    await second.controller.exportData()
    await second.controller.privacy('enable')
    assert.equal(second.calls.length, reads)
  } finally {
    mutation.resolve({ deleted_attempts: 1, deleted_mastery: 1, deleted_quizzes: 1 })
    await deletion
    await tick()
  }
  for (const item of [first, second, newcomer]) {
    assert.equal(item.controller.state.busy, false)
    assert.equal(item.controller.state.settings?.enabled, true)
    assert.equal(item.controller.state.overview?.knowledge_base_id, item.context.scope.kbId)
  }
})

test('privacy completion recovers a revisited principal without blocking another principal', async () => {
  const mutation = deferred<{ deleted_attempts: number; deleted_mastery: number; deleted_quizzes: number }>()
  const first = setup({ clear: () => mutation.promise })
  await tick()
  const deletion = first.controller.privacy('clearAll')
  first.context.scope.tenantId = 'other'
  try {
    await tick()
    assert.equal(first.controller.state.busy, false)
    assert.equal(first.controller.state.settings?.enabled, true)
    first.context.scope.tenantId = 'tenant'
    await tick()
    assert.equal(first.controller.state.busy, true)
    assert.equal(first.controller.state.settings, null)
  } finally {
    mutation.resolve({ deleted_attempts: 0, deleted_mastery: 0, deleted_quizzes: 0 })
    await deletion
    await tick()
  }
  assert.equal(first.controller.state.busy, false)
  assert.equal((first.controller.state.settings as LearningSettings | null)?.enabled, true)
})

test('consent writes settle after navigation before releasing peer cards', async () => {
  const mutation = deferred<LearningSettings>()
  const first = setup({ setEnabled: () => mutation.promise })
  const peer = setup()
  await tick()
  const changing = first.controller.privacy('disable')
  assert.equal(first.calls.find(call => call.name === 'setEnabled')?.args[1], undefined)
  first.context.scope.kbId = 'other'
  await tick()
  assert.equal(first.controller.state.busy, true)
  assert.equal(peer.controller.state.busy, true)
  first.stop()
  assert.equal(peer.controller.state.busy, true)
  mutation.resolve({ enabled: false, algorithm_version: 'bkt-v1' })
  await changing; await tick()
  assert.equal(peer.controller.state.busy, false)
})

test('privacy failures and origin unmount release other cards and discard pending personal reads', async () => {
  for (const unmount of [false, true]) {
    const mutation = deferred<{ deleted_attempts: number; deleted_mastery: number; deleted_quizzes: number }>()
    const download = deferred<Record<string, unknown>>()
    const first = setup({ clear: () => mutation.promise })
    const second = setup({ export: () => download.promise })
    await tick(); await second.controller.loadQuiz('', true)
    const exporting = second.controller.exportData(true)
    const deletion = first.controller.privacy('clearAll')
    if (unmount) first.stop()
    const newcomer = setup()
    assert.equal(newcomer.controller.state.busy, true)
    assert.equal(second.controller.state.quiz, null)
    download.resolve({ secret: 'personal data' })
    assert.equal(await exporting, undefined)
    mutation.reject({ status: 429, error: { code: 'learning_busy' } })
    await deletion; await tick()
    for (const item of [second, newcomer]) {
      assert.equal(item.controller.state.busy, false)
      assert.equal(item.controller.state.settings?.enabled, true)
    }
    if (!unmount) {
      assert.equal(first.controller.state.error, 'busy')
      assert.equal(first.controller.state.busy, false)
      await first.controller.initialize(); await tick()
      assert.equal(first.controller.state.settings?.enabled, true)
    }
    first.stop(); second.stop(); newcomer.stop()
  }
})

test('polling errors preserve the quiz reference and retry fetches it without generating another quiz', async () => {
  let failing = true
  const { controller, calls } = setup({
    prepareQuiz: async () => quiz('pending'),
    quiz: async () => {
      if (failing) throw { status: 429, error: { code: 'learning_busy' } }
      return quiz()
    },
  }, { wait: async () => {} })
  await tick(); await controller.loadQuiz('', true)
  assert.equal(controller.state.quizError, 'busy')
  assert.equal(controller.state.quizLoading, false)
  assert.equal(controller.state.quiz?.id, 'quiz')
  assert.equal(controller.state.quiz?.questions.length, 0)
  failing = false
  await controller.retryQuiz()
  assert.equal(controller.state.quizError, '')
  assert.equal(controller.state.quiz?.status, 'ready')
  assert.equal(calls.filter(call => call.name === 'prepareQuiz').length, 1)
})

test('cancelling an Agent quiz before its first response remains resumable', async () => {
  const pending = deferred<LearningQuizView>()
  let reads = 0
  const { controller, context, calls } = setup({ quiz: () => ++reads === 1 ? pending.promise : Promise.resolve(quiz()) })
  await tick()
  context.pageId = undefined
  context.quizId = 'quiz'
  await tick()
  controller.cancelQuiz()
  pending.resolve(quiz())
  await tick()
  assert.equal(controller.state.quiz, null)
  assert.equal(controller.state.paused, true)
  await controller.retryQuiz()
  assert.equal((controller.state.quiz as LearningQuizView | null)?.status, 'ready')
  assert.equal(controller.state.paused, false)
  assert.equal(calls.filter(call => call.name === 'prepareQuiz').length, 0)
})

test('recommendations and graph overlays keep only visible nodes in the current KB and recover after errors', async () => {
  let failing = true
  const recommendation = (kb = 'kb') => ({ ...node('page', kb), score: 1, reason_codes: ['review_due'],
    components: { review_need: 1, graph_frontier: 0, interest_match: 0, content_quality: 1 } })
  const { controller } = setup({
    recommendations: async () => [recommendation(), recommendation('foreign')],
    overlay: async () => {
      if (failing) throw new Error('network')
      return [node(), node('page', 'foreign'), node('hidden')]
    },
  })
  await tick()
  assert.deepEqual(controller.state.recommendations.map(item => item.knowledge_base_id), ['kb'])
  assert.equal(controller.state.overlayError, 'request')
  assert.equal(controller.state.overlay.length, 0)
  failing = false
  await controller.overlay()
  assert.equal(controller.state.overlayError, '')
  assert.deepEqual(controller.state.overlay.map(item => item.slug), ['concept/page'])
})

test('an assessment refreshes peer cards only within its tenant, user and KB', async () => {
  const first = setup()
  const peer = setup()
  const foreign = setup()
  foreign.context.scope.kbId = 'other'
  await tick(); await first.controller.loadQuiz('', true); await peer.controller.loadQuiz('', true)
  const peerCalls = peer.calls.length
  const foreignCalls = foreign.calls.length
  await first.controller.submit('question', 'a'); await tick()
  for (const name of ['overview', 'recommendations', 'overlay', 'node', 'quiz']) {
    assert.ok(peer.calls.slice(peerCalls).some(call => call.name === name), name)
  }
  assert.equal(foreign.calls.length, foreignCalls)
})

test('export results are discarded on logout and clear, even when transport ignores abort', async () => {
  const pending = deferred<Record<string, unknown>>()
  const { controller, context } = setup({ export: () => pending.promise })
  await tick()
  const download = controller.exportData(true)
  context.scope.available = false
  pending.resolve({ personal: 'secret' })
  assert.equal(await download, undefined)
  assert.equal(controller.state.exporting, false)
})

test('reload stores only scoped quiz IDs and fetches answered state fresh', async () => {
  const values = new Map<string, string>()
  const pointers = learningQuizPointers({ getItem: key => values.get(key) || null, setItem: (key, value) => { values.set(key, value) },
    removeItem: key => { values.delete(key) }, key: index => [...values.keys()][index] || null, get length() { return values.size } })
  const first = setup({}, { pointers })
  await tick(); await first.controller.loadQuiz('', true); first.stop()
  assert.deepEqual([...values.values()], ['quiz'])
  const answered = quiz(); answered.questions[0] = { ...answered.questions[0], answered: true, result }
  const second = setup({ quiz: async () => answered }, { pointers })
  await tick()
  assert.equal(second.controller.state.quiz?.questions[0].result?.correct_option, 'a')
  assert.equal(pointers.get({ ...second.context.scope, userId: 'other' }, 'page'), '')
  await second.controller.privacy('clearAll')
  assert.equal(values.size, 0)
})

test('live and persisted Agent results keep only references, never answers or questions', () => {
  const output = JSON.stringify({ display_type: 'learning_quiz', quiz_id: 'q', knowledge_base_id: 'kb', questions: [result], correct_option: 'secret' })
  for (const parsed of [learningToolReference(output), learningToolReference(output, { output, tool_name: 'prepare_quiz' }), learningToolReference(undefined, JSON.parse(output))]) {
    assert.deepEqual(parsed, { display_type: 'learning_quiz', quiz_id: 'q', knowledge_base_id: 'kb' })
  }
  for (const display_type of ['learning_profile', 'learning_recommendations']) {
    assert.equal(learningToolReference(JSON.stringify({ display_type, knowledge_base_id: 'kb', profile: result }))?.display_type, display_type)
  }
  assert.equal(learningToolReference('not JSON'), null)
})

test('identity keys cannot collide; insecure HTTP UUID fallback and aborted waits work', async () => {
  assert.notEqual(learningScopeKey({ tenantId: 'a:b', userId: 'c', kbId: 'd', available: true }), learningScopeKey({ tenantId: 'a', userId: 'b:c', kbId: 'd', available: true }))
  const id = learningAttemptId({ getRandomValues: value => { new Uint8Array(value!.buffer).fill(1); return value } })
  assert.match(id, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/)
  const controller = new AbortController()
  const wait = waitForPoll(10000, controller.signal)
  controller.abort(); await assert.rejects(wait)
  assert.equal(pollDelay(100), 10000)
})
