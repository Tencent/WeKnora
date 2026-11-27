<template>
  <span v-if="messageId" class="answer-feedback" :class="{ 'is-rated': rating !== 'none' }">
    <t-button
      size="small"
      variant="outline"
      shape="round"
      class="answer-feedback__button"
      :class="{ 'is-active': rating === 'like' }"
      :loading="pending === 'like'"
      :disabled="pending !== ''"
      :aria-pressed="rating === 'like'"
      :title="rating === 'like' ? t('answerFeedback.liked') : t('answerFeedback.like')"
      @click.stop="onLike"
    >
      <t-icon name="thumb-up" />
    </t-button>
    <t-button
      size="small"
      variant="outline"
      shape="round"
      class="answer-feedback__button"
      :class="{ 'is-active': rating === 'dislike' }"
      :loading="pending === 'dislike'"
      :disabled="pending !== ''"
      :aria-pressed="rating === 'dislike'"
      :title="rating === 'dislike' ? t('answerFeedback.disliked') : t('answerFeedback.dislike')"
      @click.stop="onDislike"
    >
      <t-icon name="thumb-down" />
    </t-button>
    <t-dialog
      v-model:visible="reasonOpen"
      :header="t('answerFeedback.dialogTitle')"
      :confirm-btn="t('answerFeedback.submit')"
      :cancel-btn="t('answerFeedback.cancel')"
      :confirm-loading="pending === 'dislike'"
      @confirm="submitDislike"
    >
      <p class="answer-feedback__hint">{{ t('answerFeedback.dialogHint') }}</p>
      <t-textarea
        v-model="reason"
        :maxlength="500"
        :placeholder="t('answerFeedback.reasonPlaceholder')"
        :autosize="{ minRows: 3, maxRows: 6 }"
      />
    </t-dialog>
  </span>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { submitAnswerFeedback } from '@/api/answerFeedback'
import { feedbackIntent, normalizeFeedbackRating, type FeedbackRating } from '../answerFeedback'

const props = defineProps<{
  sessionId?: string
  messageId?: string
  feedback?: { rating?: string; reason?: string } | null
}>()

const emit = defineEmits<{
  saved: [state: { rating: FeedbackRating; reason?: string }]
}>()

const { t } = useI18n()
const rating = ref<FeedbackRating>(normalizeFeedbackRating(props.feedback?.rating))
const reason = ref(props.feedback?.reason || '')
const reasonOpen = ref(false)
const pending = ref<'' | 'like' | 'dislike'>('')

watch(() => props.feedback, (value) => {
  rating.value = normalizeFeedbackRating(value?.rating)
  if (!reasonOpen.value) reason.value = value?.reason || ''
}, { deep: true })

const onLike = () => {
  const intent = feedbackIntent(rating.value, 'like')
  if (intent.type === 'submit') void send(intent.rating, 'like')
}

const onDislike = () => {
  const intent = feedbackIntent(rating.value, 'dislike')
  if (intent.type === 'prompt') {
    reason.value = ''
    reasonOpen.value = true
    return
  }
  void send('none', 'dislike')
}

const submitDislike = () => {
  reasonOpen.value = false
  void send('dislike', 'dislike', reason.value.trim())
}

const send = async (next: 'like' | 'dislike' | 'none', which: 'like' | 'dislike', dislikeReason = '') => {
  if (!props.sessionId || !props.messageId || pending.value) return
  const previous = rating.value
  const previousReason = reason.value
  pending.value = which
  rating.value = next
  try {
    await submitAnswerFeedback(props.sessionId, props.messageId, {
      rating: next,
      reason: next === 'dislike' ? dislikeReason : '',
    })
    reason.value = next === 'dislike' ? dislikeReason : ''
    emit('saved', { rating: next, reason: reason.value })
    MessagePlugin.success(t(next === 'none' ? 'answerFeedback.cancelled' : 'answerFeedback.saved'))
  } catch (error) {
    rating.value = previous
    reason.value = previousReason
    MessagePlugin.error(t('answerFeedback.failed'))
  } finally {
    pending.value = ''
  }
}
</script>

<style scoped>
.answer-feedback__hint {
  margin: 0 0 8px;
  color: var(--td-text-color-secondary);
}
</style>
