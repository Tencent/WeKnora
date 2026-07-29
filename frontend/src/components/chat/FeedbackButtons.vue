<template>
  <div class="feedback-buttons" :class="['feedback-buttons--' + size]">
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :class="['feedback-buttons__btn', { 'is-active': currentRating === 'like' }]"
      :disabled="disabled || pending"
      :title="$t('feedback.like')"
      @click.stop="onClick('like')"
      data-test="feedback-like-btn"
    >
      <template #icon>
        <t-icon :name="currentRating === 'like' ? 'thumb-up-filled' : 'thumb-up'" />
      </template>
    </t-button>
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :class="['feedback-buttons__btn', { 'is-active': currentRating === 'dislike' }]"
      :disabled="disabled || pending"
      :title="$t('feedback.dislike')"
      @click.stop="onClick('dislike')"
      data-test="feedback-dislike-btn"
    >
      <template #icon>
        <t-icon :name="currentRating === 'dislike' ? 'thumb-down-filled' : 'thumb-down'" />
      </template>
    </t-button>
    <!-- Separate teleport so the dialog renders above modals/settings-overlays -->
    <Teleport to="body">
      <t-dialog
        v-model:visible="dialogVisible"
        :header="$t('feedback.dislikeHeader')"
        :on-confirm="onConfirmDislike"
        :on-close="onCancelDislike"
        :close-on-overlay-click="false"
        width="520px"
        data-test="feedback-dislike-dialog"
      >
        <p class="feedback-buttons__hint">{{ $t('feedback.dislikeHint') }}</p>
        <div class="feedback-buttons__reasons">
          <t-checkbox
            v-for="reason in reasonOptions"
            :key="reason.key"
            :checked="selectedReasons.includes(reason.key)"
            :label="reason.label"
            @change="(checked) => onReasonChange(reason.key, checked)"
          />
        </div>
        <div class="feedback-buttons__comment-wrap">
          <t-textarea
            v-model="comment"
            :placeholder="$t('feedback.commentPlaceholder')"
            :maxlength="500"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :status="commentError ? 'error' : undefined"
          />
          <p v-if="commentError" class="feedback-buttons__comment-error">
            {{ commentError }}
          </p>
        </div>
      </t-dialog>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { setMessageFeedback } from '@/api/chat';

const props = defineProps({
  sessionId: { type: String, required: true },
  messageId: { type: String, required: true },
  // Caller's current rating: "like" | "dislike" | "" | undefined.
  // Pulled from the user_feedback hydration returned by the backend on
  // message load. Empty string means "no rating yet".
  modelValue: { type: String, default: '' },
  reasons: { type: Array, default: () => [] },
  comment: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
  size: { type: String, default: 'small' },
});

const emit = defineEmits(['update:modelValue', 'update:reasons', 'update:comment', 'feedback-saved']);

const { t } = useI18n();

const currentRating = computed(() => props.modelValue || '');
const pending = ref(false);
const dialogVisible = ref(false);
const selectedReasons = ref([...props.reasons]);
const comment = ref(props.comment || '');
const commentError = ref('');

// Keep the dialog's draft state in sync when the parent updates the
// hydrated rating (e.g. after a page reload). The rating itself is not
// editable inside the dialog — only the reasons and comment for a dislike.
watch(
  () => [props.reasons, props.comment],
  ([nextReasons, nextComment]) => {
    selectedReasons.value = Array.isArray(nextReasons) ? [...nextReasons] : [];
    comment.value = nextComment || '';
  },
);

// Reason preset keys. The backend validates against these so the labels here
// must stay in lockstep with the list in internal/types/message_feedback.go.
// Translations are looked up via the i18n key feedback.reasons.<key>.
const REASON_KEYS = ['inaccurate', 'irrelevant', 'outdated', 'incomplete', 'harmful', 'other'];

const reasonOptions = computed(() =>
  REASON_KEYS.map((key) => ({
    key,
    label: t(`feedback.reasons.${key}`),
  })),
);

function onReasonChange(key: string, checked: boolean) {
  if (checked) {
    if (!selectedReasons.value.includes(key)) {
      selectedReasons.value = [...selectedReasons.value, key];
    }
  } else {
    selectedReasons.value = selectedReasons.value.filter((r) => r !== key);
    if (key === 'other') {
      commentError.value = '';
    }
  }
}

function validateComment(): boolean {
  if (selectedReasons.value.includes('other')) {
    if (!comment.value.trim()) {
      commentError.value = t('feedback.commentRequiredWhenOther');
      return false;
    }
  }
  commentError.value = '';
  return true;
}

// onClick implements the optimistic-update + cancel-on-second-click flow.
// The two state transitions are dispatched without waiting for the network
// round-trip: the UI flips immediately, then either the backend confirms
// (and the optimistic value sticks) or rolls back (and we revert + toast).
// Cancelling a rating uses rating="none" so the same PUT URL covers like,
// dislike and cancel with a single code path.
async function onClick(next) {
  if (props.disabled || pending.value) return;
  const isCancel = currentRating.value === next;
  const target = isCancel ? 'none' : next;
  const previous = currentRating.value;
  const previousReasons = [...selectedReasons.value];
  const previousComment = comment.value;

  if (target === 'dislike') {
    selectedReasons.value = [...previousReasons];
    comment.value = previousComment;
    commentError.value = '';
    pending.value = true;
    emit('update:modelValue', 'dislike');
    if (previous === 'like') {
      emit('update:reasons', []);
    }
    dialogVisible.value = true;
    pending.value = false;
    return;
  }

  pending.value = true;
  emit('update:modelValue', target);
  emit('update:reasons', []);
  emit('update:comment', '');
  try {
    await setMessageFeedback({
      session_id: props.sessionId,
      message_id: props.messageId,
      rating: target,
      reasons: [],
      comment: '',
    });
    emit('feedback-saved', { rating: target, reasons: [], comment: '' });
    MessagePlugin.success(t(target === 'none' ? 'feedback.cancelSuccess' : 'feedback.saveSuccess'));
  } catch (err) {
    emit('update:modelValue', previous);
    emit('update:reasons', previousReasons);
    emit('update:comment', previousComment);
    MessagePlugin.error(t('feedback.saveError'));
  } finally {
    pending.value = false;
  }
}

async function onConfirmDislike() {
  if (pending.value) return;
  if (!validateComment()) return;
  pending.value = true;
  try {
    const apiReasons = [...selectedReasons.value];
    await setMessageFeedback({
      session_id: props.sessionId,
      message_id: props.messageId,
      rating: 'dislike',
      reasons: apiReasons,
      comment: comment.value,
    });
    emit('update:reasons', apiReasons);
    emit('update:comment', comment.value);
    emit('feedback-saved', { rating: 'dislike', reasons: apiReasons, comment: comment.value });
    dialogVisible.value = false;
    MessagePlugin.success(t('feedback.saveSuccess'));
  } catch (err) {
    emit('update:modelValue', '');
    MessagePlugin.error(t('feedback.saveError'));
  } finally {
    pending.value = false;
  }
}

function onCancelDislike() {
  // Revert the optimistic flip on cancel so the bubble stays consistent
  // with the persisted state.
  emit('update:modelValue', '');
  emit('update:reasons', []);
  emit('update:comment', '');
  dialogVisible.value = false;
}

</script>

<style scoped>
.feedback-buttons {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}
.feedback-buttons__btn.is-active {
  border-color: var(--td-brand-color, #366ef4);
  color: var(--td-brand-color, #366ef4);
  background: var(--td-brand-color-light, #e8f1ff);
}
.feedback-buttons__reasons {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin: 12px 0;
}
.feedback-buttons__hint {
  color: var(--td-text-color-secondary, #666);
  margin: 0 0 12px 0;
  font-size: 13px;
}
.feedback-buttons__comment-wrap {
  margin-top: 4px;
}
.feedback-buttons__comment-error {
  color: var(--td-error-color, #d54941);
  font-size: 12px;
  margin: 4px 0 0 0;
}
</style>
