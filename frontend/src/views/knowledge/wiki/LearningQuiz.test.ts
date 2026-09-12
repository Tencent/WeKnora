import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { readFileSync } from 'node:fs'
import { createServer, type ViteDevServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h, type Component, type SetupContext } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createI18n } from 'vue-i18n'
import en from '../../../i18n/locales/en-US'
import zh from '../../../i18n/locales/zh-CN'
import ja from '../../../i18n/locales/ja-JP'
import ko from '../../../i18n/locales/ko-KR'
import ru from '../../../i18n/locales/ru-RU'

let server: ViteDevServer
let Quiz: Component
let Panel: Component
let Renderer: Component
let Legend: Component
let Recommendations: Component
before(async () => {
  server = await createServer({
    configFile: false, optimizeDeps: { noDiscovery: true, entries: [] },
    plugins: [{ name: 'learning-component-isolation', enforce: 'pre', load(id) {
      if (id.endsWith('/LearningResult.vue')) return '<script setup>defineProps(["data"])</script><template><div class="fresh-learning-ref" :data-quiz="data.quiz_id" :data-kb="data.knowledge_base_id" /></template>'
      if (id.endsWith('.vue') && !['ToolResultRenderer.vue', 'LearningPanel.vue', 'LearningQuiz.vue', 'LearningFeedback.vue', 'LearningStateBadge.vue', 'LearningRecommendations.vue', 'LearningLegend.vue'].some(name => id.endsWith(`/${name}`))) return '<template><div /></template>'
    } }, vue()],
    resolve: { alias: { '@': fileURLToPath(new URL('../../../', import.meta.url)) } },
    server: { middlewareMode: true, hmr: false }, appType: 'custom',
  })
  Quiz = (await server.ssrLoadModule('/src/views/knowledge/wiki/LearningQuiz.vue')).default
  Panel = (await server.ssrLoadModule('/src/views/knowledge/wiki/LearningPanel.vue')).default
  Renderer = (await server.ssrLoadModule('/src/views/chat/components/ToolResultRenderer.vue')).default
  Legend = (await server.ssrLoadModule('/src/views/knowledge/wiki/LearningLegend.vue')).default
  Recommendations = (await server.ssrLoadModule('/src/views/knowledge/wiki/LearningRecommendations.vue')).default
})
after(async () => { await server?.close() })
const mastery = { state: 'learning', p_mastery: 0.6, attempts: 1, correct: 1, consecutive_correct: 1, source_stale: false }
const result = { question_id: 'question', selected_option: 'b', correct_option: 'a', correct: false,
  explanation: 'SERVER_ONLY_EXPLANATION', evidence: [{ knowledge_id: 'source', chunk_id: 'chunk', quote: '<script>source evidence</script>' }], mastery }
const question = { id: 'question', prompt: '<script>Question?</script>', options: ['a', 'b', 'c', 'd'].map(id => ({ id, text: `Option ${id}` })), answered: false }
function controller(overrides: Record<string, unknown> = {}) {
  return { state: {
    settings: { enabled: true, algorithm_version: 'v1' }, attempts: {}, recommendations: [], epoch: 1,
    quiz: { id: 'quiz', status: 'ready', slug: 'concept/page', title: 'Topic', questions: [{ ...question }] },
    ...overrides,
  } }
}
async function render(component: Component, props: Record<string, unknown>, locale = 'en-US') {
  const app = createSSRApp({ render: () => h(component, props) })
  app.use(createI18n({ legacy: false, locale, messages: { 'en-US': en, 'zh-CN': zh, 'ja-JP': ja, 'ko-KR': ko, 'ru-RU': ru } }))
  for (const name of ['t-icon', 't-loading', 't-switch']) app.component(name, { render: () => h('span') })
  app.component('t-button', { setup: (_: unknown, { slots, attrs }: SetupContext) => () => h('button', attrs, slots.default?.()) })
  app.component('t-tooltip', { setup: (_: unknown, { slots, attrs }: SetupContext) => () => h('span', attrs, slots.default?.()) })
  app.component('t-dropdown', { setup: (_: unknown, { slots, attrs }: SetupContext) => () => h('div', { 'data-options': JSON.stringify(attrs.options) }, slots.default?.()) })
  app.component('t-dialog', { render: () => h('div') })
  return renderToString(app)
}

test('unanswered render is escaped, accessible and contains no answer keys or feedback', async () => {
  const html = await render(Quiz, { controller: controller(), canPrepare: true })
  assert.match(html, /type="radio"/)
  assert.match(html, /<fieldset/)
  assert.match(html, /&lt;script&gt;Question/)
  assert.doesNotMatch(html, /SERVER_ONLY|answer-result|Correct answer|<script>/)
  const malformed = controller()
  ;(malformed.state.quiz.questions[0] as any).result = result
  const leaked = await render(Quiz, { controller: malformed })
  assert.doesNotMatch(leaked, /SERVER_ONLY|source evidence|class="quiz-option correct"/)
})

test('answered reload shows saved choice, correct choice, explanation and source drillthrough', async () => {
  const html = await render(Quiz, { controller: controller({ quiz: { id: 'quiz', status: 'ready', questions: [{ ...question, answered: true, result }] } }) })
  assert.match(html, /SERVER_ONLY_EXPLANATION/)
  assert.match(html, /Incorrect/)
  assert.match(html, /Source 1/)
  assert.match(html, /&lt;script&gt;source evidence&lt;\/script&gt;/)
  assert.match(html, /<fieldset disabled/)
  assert.doesNotMatch(html, /Submit answer|<script>/)
})

test('pending, failed, stale, paused and error states expose the expected controls', async () => {
  const completed = controller({ quiz: { id: 'quiz', status: 'ready', questions: [{ ...question, answered: true, result }] } })
  const done = await render(Quiz, { controller: completed })
  assert.match(done, /New practice/)
  const next = await render(Quiz, { controller: controller({ previousQuiz: completed.state.quiz, quiz: { id: 'next', status: 'pending', questions: [] } }) })
  assert.match(next, /Previous practice/)
  assert.match(next, /SERVER_ONLY_EXPLANATION/)
  assert.match(next, /Your answer: Option b/)
  const pending = await render(Quiz, { controller: controller({ quizLoading: true, quiz: { status: 'running', questions: [] } }) })
  assert.match(pending, /Preparing quiz|Stop waiting/)
  assert.doesNotMatch(pending, /type="radio"/)
  for (const status of ['stale', 'failed']) {
    const html = await render(Quiz, { controller: controller({ quiz: { status, questions: [] } }) })
    assert.match(html, /Prepare fresh quiz/)
  }
  const paused = await render(Quiz, { controller: controller({ paused: true, quiz: { id: 'q', status: 'pending', questions: [] } }) })
  assert.match(paused, /Status checks paused/)
  assert.match(paused, /Check again/)
  const error = await render(Quiz, { controller: controller({ quizError: 'request' }) })
  assert.match(error, /role="alert"/)
  assert.match(error, /could not be loaded/)
})

test('privacy controls render when disabled and every learning locale resolves', async () => {
  const html = await render(Panel, { controller: controller({ settings: { enabled: false } }), scopeKey: 'tenant:user:kb' })
  assert.match(html, /<button[^>]*type="button"[^>]*role="switch"[^>]*aria-checked="false"/)
  assert.match(html, /aria-label="Personal learning history"/)
  assert.match(html, /Export all learning data/)
  assert.match(html, /Clear this knowledge base/)
  assert.doesNotMatch(html, /type="radio"/)
  for (const locale of ['en-US', 'zh-CN', 'ja-JP', 'ko-KR', 'ru-RU']) {
    const output = await render(Panel, { controller: controller(), scopeKey: 'scope', page: { title: 'Topic' } }, locale)
    assert.doesNotMatch(output, /learning\.(title|submit|privacy|overview)/)
  }
})

test('an Agent panel keeps the resume control after cancelling its initial quiz read', async () => {
  const html = await render(Panel, {
    controller: controller({ quiz: null, quizLoading: false, paused: true }),
    scopeKey: 'tenant:user:kb',
    compact: true,
  })
  assert.match(html, /Status checks paused/)
  assert.match(html, /Check again/)
})

test('graph legend distinguishes all four mastery states and recommendations explain familiarity separately', async () => {
  const legend = await render(Legend, {})
  for (const label of ['Unassessed', 'Learning', 'Mastered', 'Review due']) assert.ok(legend.includes(label), label)
  for (const color of ['#8c8c8c', '#0052d9', '#2ba471', '#e37318']) assert.ok(legend.includes(color), color)
  const html = await render(Recommendations, { items: [{
    page_id: 'page', slug: 'concept/page', title: '<script>Topic</script>', familiar: true,
    mastery: { ...mastery, source_stale: true }, reason_codes: ['graph_frontier', '<unknown>'],
  }] })
  assert.match(html, /&lt;script&gt;Topic&lt;\/script&gt;/)
  assert.match(html, /Related topic/)
  assert.match(html, /&lt;unknown&gt;/)
  assert.match(html, /Reading and source use indicate familiarity, not mastery/)
  assert.match(html, /Model estimate: 60% \(uncalibrated\)/)
  assert.match(html, /Sources changed/)
  assert.doesNotMatch(html, /<script>|<unknown>/)
})

test('live and persisted tool results render fresh HTTP reference cards, not raw payloads', async () => {
  for (const display_type of ['learning_quiz', 'learning_profile', 'learning_recommendations']) {
    const payload = { display_type, quiz_id: 'quiz', knowledge_base_id: 'kb', questions: [result], profile: { secret: 'SECRET' } }
    for (const props of [
      { displayType: display_type, toolData: payload },
      { displayType: display_type, output: JSON.stringify(payload), toolData: { tool_name: 'tool', success: true } },
      { output: JSON.stringify(payload) },
    ]) {
      const html = await render(Renderer, props)
      assert.match(html, /fresh-learning-ref/)
      assert.match(html, /data-kb="kb"/)
      assert.doesNotMatch(html, /fallback-output|SERVER_ONLY|SECRET|correct_option/)
    }
  }
})

test('failed or incomplete learning tool results keep the original error output', async () => {
  const failed = await render(Renderer, {
    displayType: 'learning_quiz',
    toolData: {},
    output: 'learning_evidence: source quotes are insufficient',
    success: false,
  })
  assert.match(failed, /fallback-output/)
  assert.match(failed, /learning_evidence: source quotes are insufficient/)
  assert.doesNotMatch(failed, /fresh-learning-ref/)

  const incomplete = await render(Renderer, {
    displayType: 'learning_quiz',
    toolData: { knowledge_base_id: 'kb' },
    success: true,
  })
  assert.match(incomplete, /fallback-output/)
  assert.doesNotMatch(incomplete, /fresh-learning-ref/)
  const stream = readFileSync(new URL('../../chat/components/AgentStreamDisplay.vue', import.meta.url), 'utf8')
  assert.match(stream, /learningType && event\?\.success !== false/)
  assert.match(stream, /event\.output \|\| event\.error/)
})

test('Wiki integration overlays existing graph nodes and scopes reader versus graph selection', () => {
  const wiki = readFileSync(new URL('./WikiBrowser.vue', import.meta.url), 'utf8')
  const state = readFileSync(new URL('../../../composables/useLearningState.ts', import.meta.url), 'utf8')
  assert.match(wiki, /graphDrawerVisible\.value \? graphDrawerPage\.value : null/)
  assert.match(wiki, /pageId: learningPage\.value\?\.id/)
  assert.match(wiki, /selectedPage\.value\?\.slug !== slug[\s\S]*await navigateToSlug\(slug\)/)
  assert.match(wiki, /watch\(\(\) => learning\.state\.overlay, applyLearningOverlay/)
  assert.match(wiki, /\.node-learning-state/)
  assert.doesNotMatch(state, /getWikiGraph|loadGraph|renderGraph/)
  const shell = readFileSync(new URL('../../platform/index.vue', import.meta.url), 'utf8')
  assert.match(shell, /@media \(max-width: 760px\)/)
  assert.match(shell, /\.main:has\(\.wiki-main-area\) \{\s+min-width: 0/)
  const kb = readFileSync(new URL('../KnowledgeBase.vue', import.meta.url), 'utf8')
  assert.match(kb, /knowledge-layout\.knowledge-layout--wiki \{ width: 100%; min-width: 0/)
  assert.match(wiki, /\.wiki-browser \.wiki-reader-title-row \{ flex-direction: column/)
})
