<template>
    <div class="feedback-buttons">
        <t-tooltip :content="feedbackType === 'like' ? $t('chat.feedbackCancelLike') : $t('chat.feedbackLike')" placement="top">
            <t-button
                size="small"
                variant="outline"
                shape="round"
                :theme="feedbackType === 'like' ? 'primary' : 'default'"
                :class="{ 'feedback-active': feedbackType === 'like' }"
                @click.stop="handleLike"
                :disabled="disabled"
            >
                <t-icon name="thumb-up" />
            </t-button>
        </t-tooltip>
        <t-tooltip :content="$t('chat.feedbackDislike')" placement="top">
            <t-button
                size="small"
                variant="outline"
                shape="round"
                :theme="feedbackType === 'dislike' ? 'danger' : 'default'"
                :class="{ 'feedback-active': feedbackType === 'dislike' }"
                @click.stop="handleDislikeClick"
                :disabled="disabled"
            >
                <t-icon name="thumb-down" />
            </t-button>
        </t-tooltip>
    </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { submitFeedback } from '@/api/chat/index';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

const props = defineProps<{
    sessionId: string;
    messageId: string;
    initialFeedback?: { type?: string; reason?: string } | null;
    disabled?: boolean;
}>();

const emit = defineEmits<{
    (e: 'feedback-change', feedback: { type: string; reason?: string }): void;
    (e: 'show-dislike-modal', messageId: string): void;
}>();

const feedbackType = ref<string>(props.initialFeedback?.type || '');
const loading = ref(false);

watch(() => props.initialFeedback, (val) => {
    if (val?.type) feedbackType.value = val.type;
}, { immediate: true });

const handleLike = async () => {
    if (loading.value) return;
    loading.value = true;
    try {
        const newType = feedbackType.value === 'like' ? 'none' : 'like';
        await submitFeedback(props.sessionId, props.messageId, { type: newType });
        feedbackType.value = newType;
        emit('feedback-change', { type: newType });
    } catch (err) {
        console.error('Submit feedback failed:', err);
    } finally {
        loading.value = false;
    }
};

const handleDislikeClick = () => {
    if (feedbackType.value === 'dislike') {
        handleCancelDislike();
        return;
    }
    emit('show-dislike-modal', props.messageId);
};

const handleCancelDislike = async () => {
    if (loading.value) return;
    loading.value = true;
    try {
        await submitFeedback(props.sessionId, props.messageId, { type: 'none' });
        feedbackType.value = '';
        emit('feedback-change', { type: 'none' });
    } catch (err) {
        console.error('Cancel feedback failed:', err);
    } finally {
        loading.value = false;
    }
};

const submitDislike = async (reason: string) => {
    if (loading.value) return;
    loading.value = true;
    try {
        await submitFeedback(props.sessionId, props.messageId, { type: 'dislike', reason });
        feedbackType.value = 'dislike';
        emit('feedback-change', { type: 'dislike', reason });
    } catch (err) {
        console.error('Submit dislike failed:', err);
    } finally {
        loading.value = false;
    }
};

defineExpose({ submitDislike });
</script>

<style scoped>
.feedback-buttons { display: flex; align-items: center; gap: 4px; }
.feedback-active { border-color: var(--td-brand-color); color: var(--td-brand-color); }
</style>
