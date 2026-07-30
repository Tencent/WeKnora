import { createApp, defineComponent, h, reactive } from 'vue'
import { createI18n } from 'vue-i18n'
import TDesign from 'tdesign-vue-next'
import 'tdesign-vue-next/es/style/index.css'
import AnswerFeedbackControls from '@/views/chat/components/AnswerFeedbackControls.vue'
import ChunkFeedbackGovernance from '@/views/knowledge/settings/ChunkFeedbackGovernance.vue'

const messages = {
  en: {
    common: { refresh: 'Refresh' },
    feedback: {
      answerLabel: 'Answer feedback',
      like: 'Like',
      dislike: 'Dislike',
      reasonLabel: 'Choose a reason',
      reasons: {
        inaccurate: 'Inaccurate',
        irrelevant: 'Irrelevant',
        incomplete: 'Incomplete',
        outdated: 'Outdated',
        other: 'Other',
      },
      saveFailed: 'Save failed',
      effectiveWeight: 'Effective',
      storedWeight: 'Stored',
      resetSuccess: 'Reset complete',
      resetFailed: 'Reset failed',
      governance: {
        title: 'Chunk feedback governance',
        description: 'Review and reset chunk feedback.',
        searchPlaceholder: 'Search',
        status: {
          all: 'All',
          rated: 'Rated',
          high: 'High',
          normal: 'Normal',
          low: 'Low',
          unrated: 'Unrated',
        },
        optimization: { all: 'All', yes: 'Needs review', no: 'Healthy' },
        columns: {
          document: 'Document',
          content: 'Content',
          ratings: 'Ratings',
          weights: 'Weights',
          status: 'Status',
          time: 'Time',
          trigger: 'Trigger',
          change: 'Change',
        },
        untitled: 'Untitled',
        needsOptimization: 'Needs review',
        healthy: 'Healthy',
        viewDetail: 'View',
        loadFailed: 'Load failed',
        detailTitle: 'Feedback detail',
        resetConfirm: 'Reset only this chunk?',
        reset: 'Reset',
        likes: 'Likes',
        dislikes: 'Dislikes',
        chunkContent: 'Chunk content',
        dislikeReasons: 'Dislike reasons',
        noReasons: 'No reasons',
        history: 'History',
      },
    },
  },
}

const message = reactive<Record<string, any>>({
  id: 'message-e2e',
  feedback_eligible: true,
  my_feedback: null,
})

const Harness = defineComponent({
  name: 'FeedbackE2EHarness',
  setup() {
    const updateFeedback = (feedback: unknown) => {
      message.my_feedback = feedback
    }
    return () => h('main', [
      h('h1', 'Issue 1248 feedback E2E'),
      h('section', { id: 'answer-feedback' }, [
        h(AnswerFeedbackControls, {
          sessionId: 'session-e2e',
          message,
          'onUpdate:feedback': updateFeedback,
        }),
        h('output', { id: 'feedback-state' }, message.my_feedback?.type || 'none'),
      ]),
      h('section', { id: 'governance' }, [
        h(ChunkFeedbackGovernance, { kbId: 'kb-e2e' }),
      ]),
    ])
  },
})

createApp(Harness)
  .use(TDesign)
  .use(createI18n({ legacy: false, locale: 'en', messages }))
  .mount('#app')
