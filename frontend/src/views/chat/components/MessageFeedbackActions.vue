<template>
  <template v-if="canFeedback">
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :theme="rating === 1 ? 'primary' : 'default'"
      :loading="submitting === 1"
      :disabled="submitting !== 0"
      :title="t('messageFeedback.like')"
      :aria-pressed="rating === 1"
      @click.stop="handleLike"
    >
      <t-icon name="thumb-up" />
    </t-button>
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :theme="rating === -1 ? 'danger' : 'default'"
      :loading="submitting === -1"
      :disabled="submitting !== 0"
      :title="t('messageFeedback.dislike')"
      :aria-pressed="rating === -1"
      @click.stop="handleDislike"
    >
      <t-icon name="thumb-down" />
    </t-button>
  </template>

  <t-dialog
    v-model:visible="reasonDialogVisible"
    :header="t('messageFeedback.reasonTitle')"
    attach="body"
    :z-index="2500"
    :confirm-btn="{
      content: t('common.confirm'),
      loading: submitting === -1,
      disabled: !canSubmitDislike,
    }"
    :cancel-btn="{ content: t('common.cancel'), disabled: submitting !== 0 }"
    width="480px"
    @confirm="submitDislike"
  >
    <div class="feedback-reason-form">
      <p class="feedback-reason-hint">{{ t('messageFeedback.reasonHint') }}</p>
      <t-radio-group v-model="reasonCode" class="feedback-reason-options">
        <t-radio v-for="option in reasonOptions" :key="option.value" :value="option.value">
          {{ option.label }}
        </t-radio>
      </t-radio-group>
      <t-textarea
        v-if="reasonCode === 'other'"
        v-model="reasonDetail"
        :placeholder="t('messageFeedback.reasonPlaceholder')"
        :maxlength="500"
        :autosize="{ minRows: 3, maxRows: 6 }"
      />
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  cancelMessageFeedback,
  submitMessageFeedback,
  type MessageFeedbackRating,
} from '@/api/chat'

const props = defineProps<{
  session: Record<string, any>
  sessionId: string
}>()

const { t } = useI18n()
const submitting = ref<0 | MessageFeedbackRating>(0)
const reasonDialogVisible = ref(false)
const reasonCode = ref<'inaccurate' | 'outdated' | 'incomplete' | 'irrelevant' | 'other'>('inaccurate')
const reasonDetail = ref('')

const rating = computed<number>(() => Number(props.session?.feedback?.rating || 0))
const canFeedback = computed(() =>
  Boolean(
    props.sessionId &&
    props.session?.id &&
    props.session?.is_completed &&
    Array.isArray(props.session?.knowledge_references) &&
    props.session.knowledge_references.length > 0,
  ),
)
const canSubmitDislike = computed(() =>
  submitting.value === 0 && (reasonCode.value !== 'other' || reasonDetail.value.trim().length > 0),
)
const reasonOptions = computed(() => [
  { value: 'inaccurate', label: t('messageFeedback.reasons.inaccurate') },
  { value: 'outdated', label: t('messageFeedback.reasons.outdated') },
  { value: 'incomplete', label: t('messageFeedback.reasons.incomplete') },
  { value: 'irrelevant', label: t('messageFeedback.reasons.irrelevant') },
  { value: 'other', label: t('messageFeedback.reasons.other') },
])

const updateFeedback = async (
  nextRating: MessageFeedbackRating,
  payload: { reason_code?: typeof reasonCode.value; reason_detail?: string } = {},
) => {
  submitting.value = nextRating
  try {
    const response: any = await submitMessageFeedback(props.sessionId, props.session.id, {
      rating: nextRating,
      ...payload,
    })
    props.session.feedback = response?.data || { rating: nextRating, ...payload }
    MessagePlugin.success(t('messageFeedback.saved'))
    return true
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('messageFeedback.saveFailed'))
    return false
  } finally {
    submitting.value = 0
  }
}

const cancelFeedback = async () => {
  submitting.value = rating.value === -1 ? -1 : 1
  try {
    await cancelMessageFeedback(props.sessionId, props.session.id)
    props.session.feedback = null
    MessagePlugin.success(t('messageFeedback.canceled'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('messageFeedback.saveFailed'))
  } finally {
    submitting.value = 0
  }
}

const handleLike = async () => {
  if (rating.value === 1) {
    await cancelFeedback()
    return
  }
  await updateFeedback(1)
}

const handleDislike = async () => {
  if (rating.value === -1) {
    await cancelFeedback()
    return
  }
  reasonCode.value = props.session?.feedback?.reason_code || 'inaccurate'
  reasonDetail.value = props.session?.feedback?.reason_detail || ''
  reasonDialogVisible.value = true
}

const submitDislike = async () => {
  if (!canSubmitDislike.value) return
  const saved = await updateFeedback(-1, {
    reason_code: reasonCode.value,
    reason_detail: reasonCode.value === 'other' ? reasonDetail.value.trim() : '',
  })
  if (saved) reasonDialogVisible.value = false
}
</script>

<style scoped lang="less">
.feedback-reason-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.feedback-reason-hint {
  margin: 0;
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.feedback-reason-options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
}
</style>
