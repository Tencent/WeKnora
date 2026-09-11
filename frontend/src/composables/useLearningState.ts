import { reactive, watch, onScopeDispose } from 'vue'
import type { LearningApi, LearningSettings, LearningOverview, LearningNodeView, LearningRecommendation, LearningQuizView, LearningClearResult } from '../api/learning'
import { createLearningRequests, learningScopeKey, learningPrincipalKey, learningError, publicLearningQuiz, learningQuizPointers, learningChanges, POLL_LIMIT, pollDelay, waitForPoll, type LearningScope } from './learningHelpers'
import { learningAttemptId } from './learningHelpers'

// Privacy mutations outlive individual cards and affect every KB for a principal.
const privacyMutations = new Set<string>()

export type LearningContext = { scope: LearningScope; pageId?: string; quizId?: string; slugs?: string[] }
export function useLearningState(context: () => LearningContext, api: LearningApi, options: {
  pointers?: ReturnType<typeof learningQuizPointers>
  wait?: typeof waitForPoll
  attemptId?: () => string
} = {}) {
  const pointers = options.pointers || learningQuizPointers()
  const requests = createLearningRequests()
  const origin = Symbol('learning')
  let disposed = false
  const state = reactive({
    epoch: 0,
    settings: null as LearningSettings | null,
    overview: null as LearningOverview | null,
    recommendations: [] as LearningRecommendation[],
    node: null as LearningNodeView | null,
    overlay: [] as LearningNodeView[],
    quiz: null as LearningQuizView | null,
    previousQuiz: null as LearningQuizView | null,
    loading: false, busy: false, exporting: false, quizLoading: false, paused: false,
    error: '', quizError: '', overlayError: '', pageError: '', answerError: '', submitting: '',
    clearResult: null as LearningClearResult | null,
    attempts: {} as Record<string, { option_id: string; attempt_id: string }>,
  })
  const allowed = () => {
    const { scope } = context()
    return scope.available && !!scope.tenantId && !!scope.userId && !!scope.kbId
  }
  const enabled = () => allowed() && state.settings?.enabled === true && !state.busy
  function resetPage() {
    for (const lane of ['page', 'quiz', 'answer']) requests.cancel(lane)
    state.node = null; state.quiz = null; state.quizError = ''; state.pageError = ''
    state.previousQuiz = null
    state.quizLoading = false; state.paused = false; state.submitting = ''; state.answerError = ''; state.attempts = {}
  }
  function reset() {
    state.epoch++
    requests.reset(); resetPage()
    state.settings = null; state.overview = null; state.recommendations = []; state.overlay = []
    state.loading = false; state.busy = privacyMutations.has(learningPrincipalKey(context().scope))
    state.exporting = false; state.error = ''; state.overlayError = ''; state.clearResult = null
  }
  function failure(error: unknown): string {
    const code = learningError(error)
    if (code === 'disabled' || code === 'unavailable') {
      reset()
      if (code === 'disabled') state.settings = { enabled: false, algorithm_version: '' }
      state.error = code
    }
    return code
  }
  async function overview() {
    if (!enabled()) return
    const ticket = requests.start('overview')
    const kb = context().scope.kbId
    state.loading = true; state.error = ''
    try {
      const [summary, recommendations] = await Promise.all([api.overview(kb, ticket.signal), api.recommendations(kb, ticket.signal)])
      if (!ticket.current()) return
      if (summary.knowledge_base_id !== kb) throw new Error('scope')
      if (!summary.enabled) { failure({ code: 'learning_disabled' }); return }
      state.overview = summary
      state.recommendations = (recommendations || []).filter(node => node.knowledge_base_id === kb)
    } catch (error) { if (ticket.current()) state.error = failure(error) }
    finally { if (ticket.current()) state.loading = false }
  }
  async function overlay() {
    requests.cancel('overlay')
    state.overlay = []; state.overlayError = ''
    if (!enabled()) return
    const slugs = [...new Set(context().slugs || [])]
    if (!slugs.length) return
    const ticket = requests.start('overlay')
    const kb = context().scope.kbId
    try {
      const nodes: LearningNodeView[] = []
      for (let offset = 0; offset < slugs.length; offset += 2000) {
        nodes.push(...await api.overlay(kb, slugs.slice(offset, offset + 2000), ticket.signal) || [])
        if (!ticket.current()) return
      }
      const visible = new Set(slugs)
      state.overlay = nodes.filter(node => node.knowledge_base_id === kb && visible.has(node.slug))
    } catch (error) { if (ticket.current()) state.overlayError = failure(error) }
  }
  async function loadNode(recordView = false) {
    if (!enabled()) return
    const { pageId, scope } = context()
    if (!pageId) return
    const ticket = requests.start('page')
    state.pageError = ''
    try {
      if (recordView) await api.recordView(pageId, ticket.signal)
      if (!ticket.current()) return
      const node = await api.node(pageId, ticket.signal)
      if (!ticket.current()) return
      if (node.page_id !== pageId || node.knowledge_base_id !== scope.kbId) throw new Error('scope')
      state.node = node
      if (recordView) void overlay()
    } catch (error) { if (ticket.current()) state.pageError = failure(error) }
  }
  function acceptQuiz(quiz: LearningQuizView) {
    const { scope, pageId } = context()
    if (quiz.knowledge_base_id !== scope.kbId || (pageId && quiz.page_id !== pageId)) throw new Error('scope')
    state.quiz = publicLearningQuiz(quiz)
    pointers.set(scope, quiz.page_id, quiz.id)
  }
  async function loadQuiz(quizId = '', prepare = false) {
    if (!enabled() || state.submitting) return
    const pageId = context().pageId || state.quiz?.page_id || state.previousQuiz?.page_id
    if (prepare && !pageId) return
    if (!prepare && !quizId) return
    const ticket = requests.start('quiz')
    state.quizLoading = true; state.paused = false; state.quizError = ''; state.answerError = ''
    if (prepare) {
      if (state.quiz?.status === 'ready' && state.quiz.questions.length && state.quiz.questions.every(question => question.answered)) {
        state.previousQuiz = state.quiz
      }
      state.quiz = null; state.attempts = {}
    }
    try {
      let quiz = prepare ? await api.prepareQuiz(pageId!, ticket.signal) : await api.quiz(quizId, ticket.signal)
      if (!ticket.current()) return
      acceptQuiz(quiz)
      for (let attempt = 0; quiz.status === 'pending' || quiz.status === 'running'; attempt++) {
        if (attempt >= POLL_LIMIT) { state.paused = true; break }
        await (options.wait || waitForPoll)(pollDelay(attempt), ticket.signal)
        if (!ticket.current()) return
        quiz = await api.quiz(quiz.id, ticket.signal)
        if (!ticket.current()) return
        acceptQuiz(quiz)
      }
    } catch (error) {
      if (ticket.current()) {
        state.quizError = failure(error)
        if (state.quizError === 'stale' && state.quiz) state.quiz = { ...state.quiz, status: 'stale', questions: [] }
        if (state.quizError === 'notFound') state.quiz = null
      }
    } finally { if (ticket.current()) state.quizLoading = false }
  }
  function cancelQuiz() {
    requests.cancel('quiz'); state.quizLoading = false; state.paused = true
  }
  async function submit(questionId: string, optionId: string) {
    if (!enabled() || state.submitting || state.quizLoading || state.quiz?.status !== 'ready') return
    const question = state.quiz.questions.find(item => item.id === questionId)
    if (!question || question.answered || !question.options.some(option => option.id === optionId)) return
    const previous = state.attempts[questionId]
    if (previous && previous.option_id !== optionId) return
    const attempt = previous || { option_id: optionId, attempt_id: (options.attemptId || learningAttemptId)() }
    state.attempts[questionId] = attempt
    const ticket = requests.start('answer')
    state.submitting = questionId; state.answerError = ''
    const quizId = state.quiz.id
    try {
      const result = await api.answer({ question_id: questionId, ...attempt }, ticket.signal)
      if (!ticket.current() || state.quiz?.id !== quizId) return
      if (result.question_id !== questionId || result.selected_option !== optionId) throw new Error('answer')
      state.quiz.questions = state.quiz.questions.map(item => item.id === questionId ? { ...item, answered: true, result } : item)
      delete state.attempts[questionId]
      requests.cancel('page')
      if (state.node) state.node = { ...state.node, mastery: result.mastery }
      else void loadNode()
      void overview(); void overlay()
      learningChanges.emit({ principal: learningPrincipalKey(context().scope), kbId: context().scope.kbId, origin, phase: 'assessment' })
    } catch (error) {
      if (ticket.current()) {
        state.answerError = failure(error)
        if (state.answerError === 'stale' && state.quiz) state.quiz = { ...state.quiz, status: 'stale', questions: [] }
      }
    } finally { if (ticket.current()) state.submitting = '' }
  }
  async function initialize() {
    if (!allowed() || state.busy) return
    reset()
    const ticket = requests.start('settings')
    state.loading = true; state.error = ''
    try {
      const settings = await api.settings(ticket.signal)
      if (!ticket.current()) return
      state.settings = settings
      if (settings.enabled) {
        void overview(); void overlay(); void loadNode(true)
        const { scope, pageId, quizId } = context()
        const saved = quizId || (pageId ? pointers.get(scope, pageId) : '')
        if (saved) void loadQuiz(saved)
      }
    } catch (error) { if (ticket.current()) state.error = failure(error) }
    finally { if (ticket.current()) state.loading = false }
  }
  async function refreshConsent() {
    if (!allowed() || state.busy) return
    if (!state.settings) { await initialize(); return }
    const ticket = requests.start('consentRefresh')
    try {
      const settings = await api.settings(ticket.signal)
      if (!ticket.current()) return
      // Download dialogs can focus the window. Preserve a just-opened menu
      // and pending answer unless the server actually changed consent.
      if (settings.enabled !== state.settings?.enabled) await initialize()
      else if (settings.enabled) {
        void overview(); void overlay(); void loadNode()
        if (state.quiz && !state.submitting) void loadQuiz(state.quiz.id)
      }
    } catch (error) {
      if (ticket.current()) state.error = failure(error)
    }
  }
  async function privacy(action: 'enable' | 'disable' | 'clearKB' | 'clearAll') {
    if (!allowed() || state.busy) return
    const scope = { ...context().scope }
    const principal = learningPrincipalKey(scope)
    const kbId = action === 'clearKB' ? scope.kbId : undefined
    privacyMutations.add(principal)
    reset(); state.busy = true
    pointers.clear(scope, action !== 'clearKB')
    // Every privacy mutation advances the server's profile-wide epoch.
    learningChanges.emit({ principal, origin, phase: 'invalidate' })
    const ticket = requests.start('privacy')
    try {
      if (action === 'enable' || action === 'disable') {
        // Aborting HTTP on navigation cannot cancel a committed privacy write.
        const settings = await api.setEnabled(action === 'enable')
        if (ticket.current()) state.settings = settings
      } else {
        const result = await api.clear(kbId)
        if (!ticket.current()) return
        state.clearResult = result
        // Clear may disable consent. Never restore a pre-deletion snapshot.
        const settings = await api.settings(ticket.signal)
        if (ticket.current()) state.settings = settings
      }
      if (ticket.current()) {
        state.busy = false
        if (state.settings?.enabled) {
          void overview(); void overlay(); void loadNode(action === 'enable')
          if (action === 'enable' && context().quizId) void loadQuiz(context().quizId)
        }
      }
    } catch (error) { if (ticket.current()) { state.error = failure(error); state.busy = false } }
    finally {
      privacyMutations.delete(principal)
      // Other mounted cards must recover even when this card was unmounted.
      learningChanges.emit({ principal, origin, phase: 'reload' })
      if (!disposed && state.busy && principal === learningPrincipalKey(context().scope)) {
        state.busy = false
        void initialize()
      }
    }
  }
  async function exportData(all = false) {
    if (!allowed() || state.busy || state.exporting) return
    const ticket = requests.start('export')
    state.exporting = true; state.error = ''
    try {
      const result = await api.export(all ? undefined : context().scope.kbId, ticket.signal)
      if (ticket.current()) return result
    } catch (error) { if (ticket.current()) state.error = failure(error) }
    finally { if (ticket.current()) state.exporting = false }
  }
  const unsubscribe = learningChanges.subscribe(change => {
    const { scope } = context()
    if (change.origin === origin || change.principal !== learningPrincipalKey(scope)) return
    if (change.kbId && change.kbId !== scope.kbId) return
    if (change.phase === 'invalidate') { reset(); state.busy = true; pointers.clear(scope, !change.kbId) }
    if (change.phase === 'reload') { state.busy = false; void initialize() }
    if (change.phase === 'assessment' && enabled()) {
      void overview(); void overlay(); void loadNode()
      if (state.quiz && !state.submitting) void loadQuiz(state.quiz.id)
    }
  })
  let previousScope: LearningScope | undefined
  watch(() => learningScopeKey(context().scope), () => {
    if (previousScope && (learningPrincipalKey(previousScope) !== learningPrincipalKey(context().scope)
      || (previousScope.available && !context().scope.available))) pointers.clear(previousScope, true)
    previousScope = { ...context().scope }
    reset(); void initialize()
  }, { immediate: true, flush: 'sync' })
  watch(() => JSON.stringify([context().pageId, context().quizId]), () => {
    resetPage()
    if (!enabled()) return
    void loadNode(true)
    const { scope, pageId, quizId } = context()
    const saved = quizId || (pageId ? pointers.get(scope, pageId) : '')
    if (saved) void loadQuiz(saved)
  }, { flush: 'sync' })
  watch(() => JSON.stringify(context().slugs || []), () => { void overlay() }, { flush: 'sync' })
  onScopeDispose(() => { disposed = true; unsubscribe(); reset() })
  const retryQuiz = () => loadQuiz(state.quiz?.id || context().quizId || '', !state.quiz && !context().quizId)
  return { state, initialize, refreshConsent, overview, overlay, loadNode, loadQuiz, retryQuiz, cancelQuiz, submit, privacy, exportData }
}
export type LearningController = ReturnType<typeof useLearningState>
