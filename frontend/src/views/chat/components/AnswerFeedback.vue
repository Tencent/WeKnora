<template>
    <div class="answer-feedback" @click.stop>
        <t-button size="small" variant="text" shape="round" class="feedback-btn"
            :class="{ 'feedback-btn--active': rating === 'like' }"
            :title="t('chat.feedback.rateHelpful')"
            :loading="submitting"
            @click.stop="handleLike">
            <t-icon name="thumb-up" />
            <span v-if="rating === 'like'" class="feedback-btn__label">{{ t('chat.feedback.rated') }}</span>
        </t-button>
        <t-button size="small" variant="text" shape="round" class="feedback-btn"
            :class="{ 'feedback-btn--active feedback-btn--dislike': rating === 'dislike' }"
            :title="t('chat.feedback.rateUnhelpful')"
            :loading="submitting"
            @click.stop="handleDislike">
            <t-icon name="thumb-down" />
            <span v-if="rating === 'dislike'" class="feedback-btn__label">{{ t('chat.feedback.rated') }}</span>
        </t-button>

        <t-dialog v-model:visible="dislikeVisible" :header="t('chat.feedback.dislikeTitle')"
            :confirm-btn="t('chat.feedback.submit')" :cancel-btn="t('common.cancel')"
            width="420px" @confirm="confirmDislike" :confirm-loading="submitting">
            <div class="dislike-reasons">
                <t-radio-group v-model="selectedReason" class="dislike-reasons__group">
                    <t-radio v-for="reason in presetReasons" :key="reason.value" :value="reason.value">
                        {{ reason.label }}
                    </t-radio>
                    <t-radio value="__custom__">{{ t('chat.feedback.otherReason') }}</t-radio>
                </t-radio-group>
                <t-textarea v-if="selectedReason === '__custom__'" v-model="customReason"
                    :placeholder="t('chat.feedback.reasonPlaceholder')" :maxlength="200" :autosize="{ minRows: 2, maxRows: 4 }" />
            </div>
        </t-dialog>
    </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { submitAnswerFeedback, cancelAnswerFeedback } from '@/api/chat/feedback';

const props = defineProps<{
    sessionId: string;
    messageId: string;
    initialRating?: string;
}>();

const { t } = useI18n();

const rating = ref(props.initialRating || '');
const submitting = ref(false);
const dislikeVisible = ref(false);
const selectedReason = ref('');
const customReason = ref('');

watch(
    () => props.initialRating,
    (val) => {
        rating.value = val || '';
    },
);

const presetReasons = computed(() => [
    { value: 'inaccurate', label: t('chat.feedback.reasonInaccurate') },
    { value: 'outdated', label: t('chat.feedback.reasonOutdated') },
    { value: 'irrelevant', label: t('chat.feedback.reasonIrrelevant') },
    { value: 'incomplete', label: t('chat.feedback.reasonIncomplete') },
    { value: 'no_reference', label: t('chat.feedback.reasonNoReference') },
]);

const resolveReason = () => {
    if (selectedReason.value && selectedReason.value !== '__custom__') {
        return selectedReason.value;
    }
    return customReason.value.trim();
};

const handleLike = async () => {
    if (submitting.value) return;
    submitting.value = true;
    try {
        if (rating.value === 'like') {
            await cancelAnswerFeedback(props.sessionId, props.messageId);
            rating.value = '';
            MessagePlugin.success(t('chat.feedback.cancelled'));
        } else {
            await submitAnswerFeedback(props.sessionId, props.messageId, 'like');
            rating.value = 'like';
            MessagePlugin.success(t('chat.feedback.likeSuccess'));
        }
    } catch (err) {
        console.error('submit like feedback failed:', err);
        MessagePlugin.error(t('chat.feedback.submitFailed'));
    } finally {
        submitting.value = false;
    }
};

const handleDislike = () => {
    if (submitting.value) return;
    if (rating.value === 'dislike') {
        void cancelRating();
        return;
    }
    selectedReason.value = '';
    customReason.value = '';
    dislikeVisible.value = true;
};

const confirmDislike = async () => {
    const reason = resolveReason();
    if (!reason) {
        MessagePlugin.warning(t('chat.feedback.reasonRequired'));
        return;
    }
    if (submitting.value) return;
    submitting.value = true;
    try {
        await submitAnswerFeedback(props.sessionId, props.messageId, 'dislike', reason);
        rating.value = 'dislike';
        dislikeVisible.value = false;
        MessagePlugin.success(t('chat.feedback.dislikeSuccess'));
    } catch (err) {
        console.error('submit dislike feedback failed:', err);
        MessagePlugin.error(t('chat.feedback.submitFailed'));
    } finally {
        submitting.value = false;
    }
};

const cancelRating = async () => {
    if (submitting.value) return;
    submitting.value = true;
    try {
        await cancelAnswerFeedback(props.sessionId, props.messageId);
        rating.value = '';
        MessagePlugin.success(t('chat.feedback.cancelled'));
    } catch (err) {
        console.error('cancel feedback failed:', err);
        MessagePlugin.error(t('chat.feedback.submitFailed'));
    } finally {
        submitting.value = false;
    }
};
</script>

<style scoped lang="less">
.answer-feedback {
    display: inline-flex;
    align-items: center;
    gap: 4px;

    .feedback-btn {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        color: var(--td-text-color-secondary);
        padding: 2px 8px;

        &:hover {
            color: var(--td-brand-color);
            background: var(--td-brand-color-light);
        }

        &--active {
            color: var(--td-brand-color);
            background: var(--td-brand-color-light);

            &.feedback-btn--dislike {
                color: var(--td-error-color);
                background: var(--td-error-color-light);
            }
        }

        &__label {
            font-size: 12px;
            line-height: 1;
        }
    }
}

.dislike-reasons {
    display: flex;
    flex-direction: column;
    gap: 12px;

    &__group {
        display: flex;
        flex-direction: column;
        gap: 8px;
        align-items: flex-start;
    }
}
</style>