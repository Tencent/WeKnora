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
          :key="reason"
          v-model="selectedReasons"
          :label="reason"
          :value="reason"
        />
      </div>
      <t-textarea
        v-model="comment"
        :placeholder="$t('feedback.commentPlaceholder')"
        :maxlength="500"
        :autosize="{ minRows: 2, maxRows: 4 }"
      />
    </t-dialog>
  </div>
</template>

<script setup>
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
const reasonOptions = computed(() => [
  t('feedback.reasons.inaccurate'),
  t('feedback.reasons.irrelevant'),
  t('feedback.reasons.outdated'),
  t('feedback.reasons.incomplete'),
  t('feedback.reasons.harmful'),
  t('feedback.reasons.other'),
]);

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
    // Open the dialog; the actual PUT happens on confirm.
    selectedReasons.value = [...previousReasons];
    comment.value = previousComment;
    pending.value = true;
    emit('update:modelValue', 'dislike');
    // Optimistically clear the like state if the user is flipping.
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
    // Roll back the optimistic state.
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
  pending.value = true;
  try {
    // Pull the canonical reason keys from the Chinese labels so the backend
    // gets the whitelist keys we documented in FeedbackDislikeReasons.
    const apiReasons = selectedReasons.value.map(labelToKey);
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
    // Roll back the optimistic flip to "like".
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

// Reason-key round-tripping: the i18n bundle translates the labels so the
// UI matches the user's language, but the backend stores the canonical
// whitelist keys. The labels are 1:1 with the keys here, so we just
// lowercase and underscore-fold the localised label.
function labelToKey(label) {
  if (!label) return '';
  return label
    .toString()
    .trim()
    .toLowerCase()
    .replace(/[\s/]+/g, '_');
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
</style>
