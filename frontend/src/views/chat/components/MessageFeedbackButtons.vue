<template>
  <span class="message-feedback-buttons">
    <t-tooltip :content="t('chat.feedbackLike')" placement="top">
      <t-button
        v-if="currentAction !== 'dislike'"
        size="small"
        variant="outline"
        shape="round"
        class="feedback-btn"
        :class="{ active: currentAction === 'like' }"
        :loading="submittingAction === 'like'"
        :disabled="disabled || submitting"
        @click.stop="handleLike"
      >
        <t-icon :name="currentAction === 'like' ? 'thumb-up-filled' : 'thumb-up'" />
      </t-button>
    </t-tooltip>
    <t-tooltip :content="t('chat.feedbackDislike')" placement="top">
      <t-button
        v-if="currentAction !== 'like'"
        size="small"
        variant="outline"
        shape="round"
        class="feedback-btn"
        :class="{ active: currentAction === 'dislike' }"
        :loading="submittingAction === 'dislike'"
        :disabled="disabled || submitting"
        @click.stop="handleDislike"
      >
        <t-icon :name="currentAction === 'dislike' ? 'thumb-down-filled' : 'thumb-down'" />
      </t-button>
    </t-tooltip>

    <t-dialog
      v-model:visible="dislikeDialogVisible"
      :header="t('chat.feedbackReasonTitle')"
      :confirm-btn="t('common.confirm')"
      :cancel-btn="t('common.cancel')"
      width="420px"
      @confirm="submitDislike"
    >
      <div class="feedback-reason-options">
        <t-radio-group v-model="selectedReason" variant="default-filled">
          <t-radio-button
            v-for="option in reasonOptions"
            :key="option.value"
            :value="option.value"
            class="feedback-reason-option"
          >
            <span class="feedback-reason-option__title">{{ option.label }}</span>
            <span class="feedback-reason-option__desc">{{ option.description }}</span>
          </t-radio-button>
        </t-radio-group>
      </div>
      <t-textarea
        v-model="dislikeReason"
        :placeholder="t('chat.feedbackReasonPlaceholder')"
        :autosize="{ minRows: 3, maxRows: 5 }"
        :maxlength="1000"
      />
    </t-dialog>
  </span>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import {
  cancelMessageFeedback,
  submitMessageFeedback,
  type MessageFeedbackAction,
} from '@/api/chat';

const props = defineProps<{
  sessionId?: string;
  message: any;
  disabled?: boolean;
}>();

const { t } = useI18n();
const currentAction = ref<MessageFeedbackAction | ''>(
  (props.message?.feedback_action || props.message?.feedback?.action || '') as MessageFeedbackAction | '',
);
const dislikeReason = ref<string>(props.message?.feedback_reason || props.message?.feedback?.reason || '');
const selectedReason = ref<string>('');
const dislikeDialogVisible = ref(false);
const submittingAction = ref<MessageFeedbackAction | ''>('');
const submitting = computed(() => submittingAction.value !== '');
const reasonOptions = computed(() => [
  {
    value: t('chat.feedbackReasonOptionMismatch'),
    label: t('chat.feedbackReasonOptionMismatch'),
    description: t('chat.feedbackReasonOptionMismatchDesc'),
  },
  {
    value: t('chat.feedbackReasonOptionWrong'),
    label: t('chat.feedbackReasonOptionWrong'),
    description: t('chat.feedbackReasonOptionWrongDesc'),
  },
  {
    value: t('chat.feedbackReasonOptionTooLong'),
    label: t('chat.feedbackReasonOptionTooLong'),
    description: t('chat.feedbackReasonOptionTooLongDesc'),
  },
  {
    value: t('chat.feedbackReasonOptionTooHard'),
    label: t('chat.feedbackReasonOptionTooHard'),
    description: t('chat.feedbackReasonOptionTooHardDesc'),
  },
  {
    value: t('chat.feedbackReasonOptionMissing'),
    label: t('chat.feedbackReasonOptionMissing'),
    description: t('chat.feedbackReasonOptionMissingDesc'),
  },
  {
    value: t('chat.feedbackReasonOptionOther'),
    label: t('chat.feedbackReasonOptionOther'),
    description: t('chat.feedbackReasonOptionOtherDesc'),
  },
]);

watch(
  () => [
    props.message?.id,
    props.message?.feedback_action,
    props.message?.feedback_reason,
    props.message?.feedback?.action,
    props.message?.feedback?.reason,
  ],
  () => {
    currentAction.value = (props.message?.feedback_action || props.message?.feedback?.action || '') as MessageFeedbackAction | '';
    dislikeReason.value = props.message?.feedback_reason || props.message?.feedback?.reason || '';
  },
  { immediate: true },
);

const messageId = computed(() => props.message?.id || props.message?.assistant_message_id || '');

const ensureTarget = () => {
  if (!props.sessionId || !messageId.value) {
    MessagePlugin.warning(t('chat.feedbackUnavailable'));
    return false;
  }
  return true;
};

const setLocalFeedback = (action: MessageFeedbackAction | '', reason = '') => {
  currentAction.value = action;
  dislikeReason.value = reason;
  if (props.message) {
    props.message.feedback_action = action;
    props.message.feedback_reason = reason;
  }
};

const submitAction = async (action: MessageFeedbackAction, reason = '') => {
  if (!ensureTarget()) return;
  submittingAction.value = action;
  try {
    await submitMessageFeedback(props.sessionId!, messageId.value, { action, reason });
    setLocalFeedback(action, reason);
    MessagePlugin.success({
      content: action === 'like' ? t('chat.feedbackThanks') : t('chat.feedbackSubmitted'),
      duration: 3000,
      closeBtn: true,
    });
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('chat.feedbackFailed'));
  } finally {
    submittingAction.value = '';
  }
};

const cancelFeedback = async (action: MessageFeedbackAction) => {
  if (!ensureTarget()) return;
  submittingAction.value = action;
  try {
    await cancelMessageFeedback(props.sessionId!, messageId.value);
    setLocalFeedback('');
    MessagePlugin.success(t('chat.feedbackCanceled'));
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('chat.feedbackFailed'));
  } finally {
    submittingAction.value = '';
  }
};

const handleLike = () => {
  if (currentAction.value === 'like') {
    cancelFeedback('like');
    return;
  }
  submitAction('like');
};

const handleDislike = () => {
  if (currentAction.value === 'dislike') {
    cancelFeedback('dislike');
    return;
  }
  selectedReason.value = '';
  dislikeReason.value = '';
  dislikeDialogVisible.value = true;
};

const submitDislike = () => {
  dislikeDialogVisible.value = false;
  const detail = dislikeReason.value.trim();
  const reason = [selectedReason.value, detail].filter(Boolean).join('：');
  submitAction('dislike', reason);
};
</script>

<style scoped lang="less">
.message-feedback-buttons {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.feedback-btn {
  color: var(--td-text-color-secondary);

  &.active {
    color: var(--td-brand-color);
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.feedback-reason-options {
  margin-bottom: 12px;

  :deep(.t-radio-group) {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
  }

  :deep(.t-radio-button) {
    height: auto;
    padding: 8px 10px;
    border-radius: 6px;
    white-space: normal;
  }
}

.feedback-reason-option {
  display: flex;
}

.feedback-reason-option__title,
.feedback-reason-option__desc {
  display: block;
  line-height: 1.35;
}

.feedback-reason-option__title {
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.feedback-reason-option__desc {
  margin-top: 2px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}
</style>
