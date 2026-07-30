<template>
  <div class="chunk-feedback" :class="{ embedded: embeddedMode }">
    <div class="feedback-buttons" v-if="!embeddedMode || showButtons">
      <!-- Like button -->
      <t-tooltip :content="likeTooltip" placement="top">
        <t-button
          size="small"
          variant="outline"
          shape="round"
          :class="['feedback-btn', 'like-btn', { active: currentFeedback === 'like' }]"
          @click.stop="handleLike"
          :disabled="loading"
        >
          <template #icon>
            <t-icon :name="currentFeedback === 'like' ? 'thumb-up' : 'thumb-up'" />
          </template>
          <span v-if="showCount && stats" class="count">{{ stats.like_count }}</span>
        </t-button>
      </t-tooltip>

      <!-- Dislike button -->
      <t-tooltip :content="dislikeTooltip" placement="top">
        <t-button
          size="small"
          variant="outline"
          shape="round"
          :class="['feedback-btn', 'dislike-btn', { active: currentFeedback === 'dislike' }]"
          @click.stop="handleDislike"
          :disabled="loading"
        >
          <template #icon>
            <t-icon :name="currentFeedback === 'dislike' ? 'thumb-down' : 'thumb-down'" />
          </template>
          <span v-if="showCount && stats" class="count">{{ stats.dislike_count }}</span>
        </t-button>
      </t-tooltip>
    </div>

    <!-- Dislike reason dialog -->
    <t-dialog
      v-model:visible="dislikeDialogVisible"
      header="选择点踩原因"
      :footer="false"
      width="400px"
      :close-on-overlay-click="true"
      @close="dislikeDialogVisible = false"
    >
      <div class="dislike-reason-form">
        <t-radio-group v-model="selectedReason" class="reason-group">
          <t-radio value="inaccurate">
            <span class="reason-label">内容不准确</span>
            <span class="reason-desc">信息与事实不符</span>
          </t-radio>
          <t-radio value="incomplete">
            <span class="reason-label">内容不完整</span>
            <span class="reason-desc">缺少重要信息</span>
          </t-radio>
          <t-radio value="irrelevant">
            <span class="reason-label">不相关</span>
            <span class="reason-desc">回答与问题无关</span>
          </t-radio>
          <t-radio value="other">
            <span class="reason-label">其他原因</span>
            <span class="reason-desc">其他问题</span>
          </t-radio>
        </t-radio-group>

        <!-- Detail input for "other" reason -->
        <t-input
          v-if="selectedReason === 'other'"
          v-model="reasonDetail"
          placeholder="请详细说明原因..."
          type="textarea"
          :rows="3"
          class="reason-detail-input"
        />

        <div class="dialog-actions">
          <t-button variant="outline" @click="dislikeDialogVisible = false">取消</t-button>
          <t-button theme="primary" @click="submitDislike" :loading="submitting">提交</t-button>
        </div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { submitFeedback, getChunkStats, type ChunkStatsDetail, type FeedbackType } from '@/api/chunk-feedback';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

interface Props {
  sessionId: string;
  messageId: string;
  embeddedMode?: boolean;
  showButtons?: boolean;
  showCount?: boolean;
  kbId?: string;
}

const props = withDefaults(defineProps<Props>(), {
  embeddedMode: false,
  showButtons: true,
  showCount: true,
});

const emit = defineEmits<{
  (e: 'feedback-change', feedback: FeedbackType | null): void;
  (e: 'stats-update', stats: ChunkStatsDetail): void;
}>();

const loading = ref(false);
const submitting = ref(false);
const currentFeedback = ref<FeedbackType | null>(null);
const dislikeDialogVisible = ref(false);
const selectedReason = ref<string>('inaccurate');
const reasonDetail = ref('');
const stats = ref<ChunkStatsDetail | null>(null);

const likeTooltip = computed(() => {
  if (currentFeedback.value === 'like') {
    return t('chat.feedback.cancelLike');
  }
  return t('chat.feedback.like');
});

const dislikeTooltip = computed(() => {
  if (currentFeedback.value === 'dislike') {
    return t('chat.feedback.cancelDislike');
  }
  return t('chat.feedback.dislike');
});

// Load stats when messageId changes
watch(
  () => props.messageId,
  async (newMessageId) => {
    if (newMessageId && props.showCount) {
      await loadStats();
    }
  },
  { immediate: true }
);

async function loadStats() {
  if (!props.messageId) return;

  try {
    const data = await getChunkStats(props.messageId);
    stats.value = data;
    emit('stats-update', data);
  } catch (error) {
    console.error('Failed to load chunk stats:', error);
    // Initialize with default values
    stats.value = {
      like_count: 0,
      dislike_count: 0,
      like_rate: 0,
      recall_weight: 1.0,
      is_pending_optimization: false,
      related_session_count: 0,
      dislike_reason_stats: {},
    };
  }
}

async function handleLike() {
  if (loading.value) return;

  loading.value = true;
  try {
    const newFeedback: FeedbackType = currentFeedback.value === 'like' ? 'unlike' : 'like';

    await submitFeedback({
      session_id: props.sessionId,
      message_id: props.messageId,
      feedback_type: newFeedback,
    });

    currentFeedback.value = newFeedback === 'unlike' ? null : newFeedback;
    emit('feedback-change', currentFeedback.value);

    // Update local stats
    if (stats.value) {
      if (newFeedback === 'like') {
        stats.value.like_count++;
      } else {
        stats.value.like_count = Math.max(0, stats.value.like_count - 1);
      }
    }

    // Reload stats to get accurate count
    await loadStats();
  } catch (error) {
    console.error('Failed to submit feedback:', error);
    MessagePlugin.error(t('chat.feedback.submitFailed'));
  } finally {
    loading.value = false;
  }
}

function handleDislike() {
  if (loading.value) return;

  if (currentFeedback.value === 'dislike') {
    // Cancel dislike
    submitUnlike();
  } else {
    // Show reason dialog
    dislikeDialogVisible.value = true;
  }
}

async function submitDislike() {
  submitting.value = true;
  try {
    await submitFeedback({
      session_id: props.sessionId,
      message_id: props.messageId,
      feedback_type: 'dislike',
      dislike_reason: selectedReason.value as any,
      dislike_reason_detail: selectedReason.value === 'other' ? reasonDetail.value : undefined,
    });

    currentFeedback.value = 'dislike';
    emit('feedback-change', currentFeedback.value);

    // Update local stats
    if (stats.value) {
      stats.value.dislike_count++;
    }

    // Reload stats
    await loadStats();

    dislikeDialogVisible.value = false;
    MessagePlugin.success(t('chat.feedback.submitSuccess'));
  } catch (error) {
    console.error('Failed to submit dislike:', error);
    MessagePlugin.error(t('chat.feedback.submitFailed'));
  } finally {
    submitting.value = false;
  }
}

async function submitUnlike() {
  loading.value = true;
  try {
    await submitFeedback({
      session_id: props.sessionId,
      message_id: props.messageId,
      feedback_type: 'undislike',
    });

    currentFeedback.value = null;
    emit('feedback-change', null);

    // Update local stats
    if (stats.value) {
      stats.value.dislike_count = Math.max(0, stats.value.dislike_count - 1);
    }

    // Reload stats
    await loadStats();
  } catch (error) {
    console.error('Failed to submit undislike:', error);
    MessagePlugin.error(t('chat.feedback.submitFailed'));
  } finally {
    loading.value = false;
  }
}
</script>

<style lang="less" scoped>
.chunk-feedback {
  display: inline-flex;
  align-items: center;

  &.embedded {
    .feedback-buttons {
      gap: 4px;
    }

    .feedback-btn {
      padding: 4px 8px;
      font-size: 12px;
    }
  }
}

.feedback-buttons {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.feedback-btn {
  transition: all 0.2s ease;

  &.like-btn {
    &.active {
      color: var(--td-success-color) !important;
      border-color: var(--td-success-color) !important;
      background-color: rgba(46, 194, 113, 0.1) !important;
    }
  }

  &.dislike-btn {
    &.active {
      color: var(--td-error-color) !important;
      border-color: var(--td-error-color) !important;
      background-color: rgba(242, 98, 75, 0.1) !important;
    }
  }

  .count {
    margin-left: 4px;
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  &:hover {
    transform: scale(1.05);
  }

  &:active {
    transform: scale(0.95);
  }
}

.dislike-reason-form {
  .reason-group {
    display: flex;
    flex-direction: column;
    gap: 12px;
    width: 100%;

    :deep(.t-radio) {
      display: flex;
      align-items: flex-start;
      padding: 12px;
      border: 1px solid var(--td-component-stroke);
      border-radius: 8px;
      transition: all 0.2s ease;

      &:hover {
        border-color: var(--td-brand-color);
        background-color: var(--td-bg-color-container-hover);
      }

      &.is-checked {
        border-color: var(--td-brand-color);
        background-color: rgba(46, 125, 250, 0.05);
      }

      .t-radio__input {
        margin-top: 2px;
      }

      .t-radio__content {
        display: flex;
        flex-direction: column;
        gap: 2px;
      }
    }

    .reason-label {
      font-size: 14px;
      font-weight: 500;
      color: var(--td-text-color-primary);
    }

    .reason-desc {
      font-size: 12px;
      color: var(--td-text-color-secondary);
    }
  }

  .reason-detail-input {
    margin-top: 12px;
  }

  .dialog-actions {
    display: flex;
    justify-content: flex-end;
    gap: 12px;
    margin-top: 20px;
  }
}
</style>
