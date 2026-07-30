<template>
  <div v-if="visible" class="message-feedback-buttons">
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :theme="currentVote === 'like' ? 'primary' : 'default'"
      :loading="likeLoading"
      @click.stop="handleLikeClick"
      :title="$t('chat.like')"
    >
      <t-icon name="thumb-up" />
    </t-button>
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :theme="currentVote === 'dislike' ? 'primary' : 'default'"
      :loading="dislikeLoading"
      @click.stop="handleDislikeClick"
      :title="$t('chat.dislike')"
    >
      <t-icon name="thumb-down" />
    </t-button>

    <t-dialog
      v-model:visible="dislikeDialogVisible"
      :footer="false"
      width="480px"
      :close-on-overlay-click="false"
      destroy-on-close
      @close="handleCloseDislikeDialog"
    >
      <template #header>
        <span>{{ $t('chat.dislikeReasonTitle') }}</span>
      </template>
      <div class="message-feedback-dialog-body">
        <div class="dislike-reasons-label">{{ $t('chat.dislikeReasonsLabel') }}</div>
        <div class="dislike-reason-tags">
          <div
            v-for="opt in reasonOptions"
            :key="opt.value"
            class="dislike-reason-tag"
            :class="{ 'dislike-reason-tag--active': selectedReasons.includes(opt.value) }"
            @click="toggleReason(opt.value)"
          >
            {{ opt.label }}
          </div>
        </div>
        <div class="dislike-reason-supplement">
          <label class="dislike-supplement-label">{{ $t('chat.dislikeSupplementLabel') }}</label>
          <t-textarea
            v-model="supplementText"
            :maxlength="200"
            :placeholder="$t('chat.dislikeSupplementPlaceholder')"
            :autosize="{ minRows: 2, maxRows: 4 }"
          />
        </div>
      </div>
      <div class="message-feedback-dialog-footer">
        <t-button variant="outline" size="small" @click="handleCloseDislikeDialog">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="dislikeSubmitLoading" @click="submitDislike">
          {{ $t('common.confirm') }}
        </t-button>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { setMessageFeedback, cancelMessageFeedback } from '@/api/chat';

type Vote = '' | 'like' | 'dislike';

interface ReasonOption {
  value: string;
  label: string;
}

const props = defineProps<{
  sessionId: string;
  messageId: string;
  enabled: boolean;
}>();

const currentVote = ref<Vote>('');
const likeLoading = ref(false);
const dislikeLoading = ref(false);

const dislikeDialogVisible = ref(false);
const selectedReasons = ref<string[]>([]);
const supplementText = ref('');
const dislikeSubmitLoading = ref(false);

const { t } = useI18n();

const reasonOptions = computed<ReasonOption[]>(() => [
  { value: 'inaccurate', label: t('chat.dislikeReasonInaccurate') },
  { value: 'outdated', label: t('chat.dislikeReasonOutdated') },
  { value: 'irrelevant', label: t('chat.dislikeReasonIrrelevant') },
  { value: 'incomplete', label: t('chat.dislikeReasonIncomplete') },
  { value: 'cite_error', label: t('chat.dislikeReasonCiteError') },
  { value: 'other', label: t('chat.dislikeReasonOther') },
]);

const visible = computed(() => props.enabled && !!props.sessionId && !!props.messageId);

const toggleReason = (value: string) => {
  const idx = selectedReasons.value.indexOf(value);
  if (idx >= 0) {
    selectedReasons.value.splice(idx, 1);
  } else {
    selectedReasons.value.push(value);
  }
};

const buildDislikeReason = (): string => {
  const codes = selectedReasons.value.join(',');
  const supp = supplementText.value.trim();
  if (!codes && !supp) return '';
  if (!codes) return supp;
  if (!supp) return codes;
  return `${codes}|${supp}`;
};

const handleLikeClick = async () => {
  if (likeLoading.value || dislikeLoading.value) return;
  if (currentVote.value === 'like') {
    likeLoading.value = true;
    try {
      await cancelMessageFeedback(props.sessionId, props.messageId);
      currentVote.value = '';
      MessagePlugin.success(t('chat.feedbackCanceled'));
    } catch (err: any) {
      MessagePlugin.error(err?.message || t('chat.feedbackFailed'));
    } finally {
      likeLoading.value = false;
    }
    return;
  }

  likeLoading.value = true;
  try {
    await setMessageFeedback({ session_id: props.sessionId, message_id: props.messageId, vote: 'like' });
    currentVote.value = 'like';
    MessagePlugin.success(t('chat.feedbackSaved'));
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('chat.feedbackFailed'));
  } finally {
    likeLoading.value = false;
  }
};

const handleDislikeClick = () => {
  if (likeLoading.value || dislikeLoading.value) return;
  if (currentVote.value === 'dislike') {
    dislikeLoading.value = true;
    cancelMessageFeedback(props.sessionId, props.messageId)
      .then(() => {
        currentVote.value = '';
        MessagePlugin.success(t('chat.feedbackCanceled'));
      })
      .catch((err: any) => {
        MessagePlugin.error(err?.message || t('chat.feedbackFailed'));
      })
      .finally(() => {
        dislikeLoading.value = false;
      });
    return;
  }
  selectedReasons.value = [];
  supplementText.value = '';
  dislikeDialogVisible.value = true;
};

const handleCloseDislikeDialog = () => {
  if (dislikeSubmitLoading.value) return;
  dislikeDialogVisible.value = false;
  selectedReasons.value = [];
  supplementText.value = '';
};

const submitDislike = async () => {
  if (selectedReasons.value.length === 0 && !supplementText.value.trim()) {
    MessagePlugin.warning(t('chat.dislikeReasonRequired'));
    return;
  }
  const reasonStr = buildDislikeReason();
  dislikeSubmitLoading.value = true;
  try {
    await setMessageFeedback({
      session_id: props.sessionId,
      message_id: props.messageId,
      vote: 'dislike',
      dislike_reason: reasonStr,
    });
    currentVote.value = 'dislike';
    dislikeDialogVisible.value = false;
    selectedReasons.value = [];
    supplementText.value = '';
    MessagePlugin.success(t('chat.feedbackSaved'));
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('chat.feedbackFailed'));
  } finally {
    dislikeSubmitLoading.value = false;
  }
};
</script>

<style scoped>
.message-feedback-buttons {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.message-feedback-dialog-body {
  padding: 8px 0;
}

.dislike-reasons-label {
  font-size: 14px;
  font-weight: 500;
  margin-bottom: 10px;
  color: var(--td-text-color-primary);
}

.dislike-reason-supplement {
  margin-top: 4px;
}

.dislike-supplement-label {
  display: block;
  font-size: 14px;
  font-weight: 500;
  margin-bottom: 8px;
  color: var(--td-text-color-primary);
}

.message-feedback-dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}

.dislike-reason-tag {
  display: inline-flex;
  align-items: center;
  padding: 4px 14px;
  font-size: 12px;
  border-radius: 4px;
  cursor: pointer;
  user-select: none;
  border: 1px solid var(--td-border-level-2-color, #d9e1ef);
  color: var(--td-text-color-primary, #333);
  background: var(--td-bg-color-container, #fff);
  transition: all 0.2s ease;
}

.dislike-reason-tag:hover {
  border-color: #00a854;
  color: #00a854;
}

.dislike-reason-tag--active {
  border-color: #00a854;
  background: #e6f7ee;
  color: #00a854;
  font-weight: 500;
}

</style>
