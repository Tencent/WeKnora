<template>
  <span class="answer-feedback">
    <t-button size="small" variant="outline" shape="round" class="answer-feedback__button"
      :class="{ 'is-active': feedbackType === 'like' }" :loading="submitting && pending === 'like'"
      :aria-pressed="feedbackType === 'like'" :title="t('chat.feedback.like')" :aria-label="t('chat.feedback.like')"
      @click.stop="onLike">
      <ChatActionIcon name="thumb-up" />
    </t-button>
    <t-button size="small" variant="outline" shape="round" class="answer-feedback__button"
      :class="{ 'is-active': feedbackType === 'dislike' }" :loading="submitting && pending === 'dislike'"
      :aria-pressed="feedbackType === 'dislike'" :title="t('chat.feedback.dislike')"
      :aria-label="t('chat.feedback.dislike')" @click.stop="onDislike">
      <ChatActionIcon name="thumb-down" />
    </t-button>
    <t-dialog v-model:visible="dialogVisible" :header="t('chat.feedback.reasonTitle')" width="420px"
      :confirm-btn="{ content: t('chat.feedback.submit'), loading: submitting }"
      :cancel-btn="{ content: t('chat.feedback.cancel') }" @confirm="confirmDislike" @cancel="dialogVisible = false">
      <t-radio-group v-model="draftCode" class="answer-feedback__reasons">
        <t-radio v-for="option in reasonOptions" :key="option.value" :value="option.value">{{ option.label }}</t-radio>
      </t-radio-group>
      <t-textarea v-model="draftText" class="answer-feedback__text" :maxlength="1000"
        :placeholder="t('chat.feedback.reasonPlaceholder')" :autosize="{ minRows: 3, maxRows: 6 }" />
    </t-dialog>
  </span>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ChatActionIcon from '@/components/ChatActionIcon.vue'
import { submitMessageFeedback, type MessageFeedbackView } from '@/api/chat'
import { submitEmbedMessageFeedback } from '@/api/embed'

type FeedbackEcho = {
  feedback_type?: string
  reason_code?: string
  reason_text?: string
} | null

const props = defineProps<{
  sessionId: string
  messageId: string
  feedback?: FeedbackEcho
  embedChannelId?: string
  embedToken?: string
  embedSessionSig?: string
  embedVisitorId?: string
}>()

const { t } = useI18n()
const feedbackType = ref(props.feedback?.feedback_type || 'none')
const reasonCode = ref(props.feedback?.reason_code || '')
const reasonText = ref(props.feedback?.reason_text || '')
const dialogVisible = ref(false)
const draftCode = ref('inaccurate')
const draftText = ref('')
const submitting = ref(false)
const pending = ref('')

watch(() => props.feedback, (value) => {
  feedbackType.value = value?.feedback_type || 'none'
  reasonCode.value = value?.reason_code || ''
  reasonText.value = value?.reason_text || ''
}, { deep: true })

const reasonOptions = computed(() => [
  { value: 'inaccurate', label: t('chat.feedback.reasonInaccurate') },
  { value: 'irrelevant', label: t('chat.feedback.reasonIrrelevant') },
  { value: 'outdated', label: t('chat.feedback.reasonOutdated') },
  { value: 'incomplete', label: t('chat.feedback.reasonIncomplete') },
  { value: 'other', label: t('chat.feedback.reasonOther') },
])

function applyView(view: MessageFeedbackView) {
  feedbackType.value = view?.feedback_type || 'none'
  reasonCode.value = view?.reason_code || ''
  reasonText.value = view?.reason_text || ''
}

async function send(type: string, code: string, text: string) {
  if (!props.sessionId || !props.messageId || submitting.value) return
  submitting.value = true
  pending.value = type === 'none' ? feedbackType.value : type
  try {
    const body = { feedback_type: type, reason_code: code, reason_text: text }
    const result = props.embedChannelId && props.embedToken
      ? await submitEmbedMessageFeedback(
        props.embedChannelId, props.embedToken, props.sessionId, props.messageId,
        props.embedSessionSig || '', props.embedVisitorId || '', body,
      )
      : await submitMessageFeedback(props.sessionId, props.messageId, body)
    applyView(result?.data || { feedback_type: type, reason_code: code, reason_text: text })
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('chat.feedback.failed'))
  } finally {
    submitting.value = false
    pending.value = ''
  }
}

function onLike() {
  void send(feedbackType.value === 'like' ? 'none' : 'like', '', '')
}

function onDislike() {
  if (feedbackType.value === 'dislike') {
    void send('none', '', '')
    return
  }
  draftCode.value = reasonCode.value || 'inaccurate'
  draftText.value = reasonText.value || ''
  dialogVisible.value = true
}

async function confirmDislike() {
  await send('dislike', draftCode.value, draftText.value)
  if (feedbackType.value === 'dislike') dialogVisible.value = false
}
</script>

<style scoped lang="less">
.answer-feedback {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.answer-feedback__button.is-active {
  color: var(--td-brand-color);
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
}

.answer-feedback__reasons {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 12px;
}

.answer-feedback__text {
  width: 100%;
}
</style>
