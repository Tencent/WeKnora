<template>
  <div class="answer-feedback" role="group" :aria-label="t('feedback.answerLabel')">
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :theme="localFeedback?.type === 'like' ? 'primary' : 'default'"
      :loading="pending === 'like'"
      :disabled="actionsDisabled"
      :title="t('feedback.like')"
      :aria-pressed="localFeedback?.type === 'like'"
      @click.stop="submit(localFeedback?.type === 'like' ? 'none' : 'like')"
    >
      <t-icon name="thumb-up" />
    </t-button>
    <t-popup
      v-model="reasonOpen"
      trigger="click"
      placement="top-left"
      :disabled="actionsDisabled"
      @visible-change="handleReasonVisibilityChange"
    >
      <t-button
        ref="dislikeButton"
        size="small"
        variant="outline"
        shape="round"
        :theme="localFeedback?.type === 'dislike' ? 'danger' : 'default'"
        :loading="pending === 'dislike'"
        :disabled="actionsDisabled"
        :title="t('feedback.dislike')"
        :aria-pressed="localFeedback?.type === 'dislike'"
        @click.stop="handleDislikeClick"
      >
        <t-icon name="thumb-down" />
      </t-button>
      <template #content>
        <div class="answer-feedback__reasons" role="menu" :aria-label="t('feedback.reasonLabel')">
          <strong>{{ t('feedback.reasonLabel') }}</strong>
          <t-button
            v-for="reason in reasons"
            :key="reason"
            variant="text"
            size="small"
            role="menuitem"
            :disabled="actionsDisabled"
            @click.stop="chooseReason(reason)"
          >
            {{ t(`feedback.reasons.${reason}`) }}
          </t-button>
        </div>
      </template>
    </t-popup>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import {
  putMessageFeedback,
  type MessageFeedbackReason,
  type MessageFeedbackType,
} from '@/api/chat';

type FeedbackState = { type: Exclude<MessageFeedbackType, 'none'>; reason_code?: MessageFeedbackReason } | null;

const props = defineProps<{
  sessionId: string;
  message: Record<string, any>;
}>();
const emit = defineEmits<{
  'update:feedback': [feedback: FeedbackState];
}>();

const { t } = useI18n();
const reasons: MessageFeedbackReason[] = ['inaccurate', 'irrelevant', 'incomplete', 'outdated', 'other'];
const localFeedback = ref<FeedbackState>(props.message?.my_feedback ?? null);
const pending = ref<MessageFeedbackType | null>(null);
const reasonOpen = ref(false);
const dislikeButton = ref<any>(null);
const actionsDisabled = computed(
  () => pending.value !== null || props.message?.feedback_eligible !== true,
);
let targetGeneration = 0;

watch(
  [() => props.sessionId, () => props.message?.id],
  () => {
    targetGeneration += 1;
    localFeedback.value = props.message?.my_feedback ?? null;
    pending.value = null;
    reasonOpen.value = false;
  },
);

watch(
  () => props.message?.my_feedback,
  (canonical) => {
    if (pending.value === null) {
      localFeedback.value = canonical ?? null;
    }
  },
  { deep: true },
);

const handleDislikeClick = () => {
  if (actionsDisabled.value) return;
  if (localFeedback.value?.type === 'dislike') {
    reasonOpen.value = false;
    void submit('none');
  }
};

const handleReasonVisibilityChange = (visible: boolean) => {
  if (actionsDisabled.value && visible) {
    reasonOpen.value = false;
    return;
  }
  reasonOpen.value = visible;
  if (!visible) {
    void nextTick(() => {
      const button = dislikeButton.value?.$el ?? dislikeButton.value;
      button?.focus?.();
    });
  }
};

const chooseReason = (reason: MessageFeedbackReason) => {
  if (actionsDisabled.value) return;
  reasonOpen.value = false;
  void submit('dislike', reason);
};

const submit = async (type: MessageFeedbackType, reason?: MessageFeedbackReason) => {
  if (actionsDisabled.value) return;
  const generation = targetGeneration;
  const messageId = props.message.id;
  const previous = localFeedback.value;
  const optimistic: FeedbackState = type === 'none' ? null : { type, ...(reason ? { reason_code: reason } : {}) };
  localFeedback.value = optimistic;
  pending.value = type;
  try {
    const response: any = await putMessageFeedback(props.sessionId, messageId, type, reason);
    if (generation !== targetGeneration) return;
    localFeedback.value = response?.data ?? optimistic;
    emit('update:feedback', localFeedback.value);
  } catch {
    if (generation !== targetGeneration) return;
    localFeedback.value = previous;
    MessagePlugin.error(t('feedback.saveFailed'));
  } finally {
    if (generation === targetGeneration) pending.value = null;
  }
};
</script>

<style scoped lang="less">
.answer-feedback {
  display: inline-flex;
  gap: 6px;
}

.answer-feedback__reasons {
  width: min(240px, calc(100vw - 32px));
  padding: 10px;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 4px;

  strong {
    padding: 2px 8px 6px;
  }
}

@media (max-width: 390px) {
  .answer-feedback {
    gap: 4px;
  }
}
</style>
