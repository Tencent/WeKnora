<template>
    <t-button size="small" variant="outline" shape="round" class="feedback-btn"
        :class="{ 'feedback-btn--active': modelValue === 'like' }" :title="t('chat.feedback.like')"
        :disabled="submitting" @click.stop="handleLike">
        <t-icon :name="modelValue === 'like' ? 'thumb-up-filled' : 'thumb-up'" />
    </t-button>
    <t-button size="small" variant="outline" shape="round" class="feedback-btn"
        :class="{ 'feedback-btn--active feedback-btn--dislike': modelValue === 'dislike' }"
        :title="t('chat.feedback.dislike')" :disabled="submitting" @click.stop="handleDislike">
        <t-icon :name="modelValue === 'dislike' ? 'thumb-down-filled' : 'thumb-down'" />
    </t-button>

    <t-dialog v-model:visible="dialogVisible" :header="t('chat.feedback.dialogTitle')" width="480px"
        :confirm-btn="{ content: t('chat.feedback.submit'), loading: submitting }"
        :cancel-btn="t('chat.feedback.cancelBtn')" @confirm="submitDislike" @close="dialogVisible = false">
        <div class="feedback-dialog-body">
            <t-checkbox-group v-model="selectedReasons">
                <t-checkbox v-for="reason in FEEDBACK_DISLIKE_REASONS" :key="reason" :value="reason">
                    {{ t(`chat.feedback.reason.${reason}`) }}
                </t-checkbox>
            </t-checkbox-group>
            <t-textarea v-model="comment" :placeholder="t('chat.feedback.commentPlaceholder')"
                :maxlength="500" :autosize="{ minRows: 3, maxRows: 6 }" />
        </div>
    </t-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
    FEEDBACK_DISLIKE_REASONS,
    submitMessageFeedback,
    type FeedbackRating,
} from '@/api/chat/feedback';

const props = defineProps<{
    sessionId: string;
    messageId: string;
    modelValue: '' | 'like' | 'dislike';
}>();

const emit = defineEmits<{
    (e: 'update:modelValue', value: '' | 'like' | 'dislike'): void;
}>();

const { t } = useI18n();

const dialogVisible = ref(false);
const selectedReasons = ref<string[]>([]);
const comment = ref('');
const submitting = ref(false);

// Optimistic update: flip the UI immediately, roll back on failure. A single
// `submitting` gate serializes every mutation (like / dislike / cancel) so the
// persisted rating cannot diverge from the UI when clicks arrive faster than
// the network settles.
const submit = async (rating: FeedbackRating, reasons?: string[], commentText?: string) => {
    if (submitting.value) return false;
    submitting.value = true;
    const previous = props.modelValue;
    const next = rating === 'none' ? '' : rating;
    emit('update:modelValue', next);
    try {
        await submitMessageFeedback(props.sessionId, props.messageId, {
            rating,
            reasons,
            comment: commentText,
        });
        MessagePlugin.success(rating === 'none' ? t('chat.feedback.canceled') : t('chat.feedback.submitted'));
        return true;
    } catch (err) {
        console.error('Failed to submit message feedback:', err);
        emit('update:modelValue', previous);
        MessagePlugin.error(t('chat.feedback.failed'));
        return false;
    } finally {
        submitting.value = false;
    }
};

const handleLike = () => {
    if (submitting.value) return;
    submit(props.modelValue === 'like' ? 'none' : 'like');
};

const handleDislike = () => {
    if (submitting.value) return;
    if (props.modelValue === 'dislike') {
        submit('none');
        return;
    }
    selectedReasons.value = [];
    comment.value = '';
    dialogVisible.value = true;
};

const submitDislike = async () => {
    const ok = await submit('dislike', selectedReasons.value, comment.value.trim());
    if (ok) {
        dialogVisible.value = false;
    }
};
</script>

<style lang="less" scoped>
.feedback-btn--active {
    color: var(--td-brand-color);
    border-color: var(--td-brand-color);
}

.feedback-btn--dislike {
    color: var(--td-error-color);
    border-color: var(--td-error-color);
}

.feedback-dialog-body {
    display: flex;
    flex-direction: column;
    gap: 16px;

    :deep(.t-checkbox-group) {
        display: flex;
        flex-wrap: wrap;
        gap: 8px 16px;
    }
}
</style>
