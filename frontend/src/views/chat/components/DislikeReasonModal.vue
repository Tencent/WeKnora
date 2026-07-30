<template>
    <t-dialog
        v-model:visible="visible"
        :header="$t('chat.feedbackDislikeReasonTitle')"
        :confirm-btn="{ content: $t('common.submit'), theme: 'primary' }"
        :cancel-btn="$t('common.cancel')"
        @confirm="handleConfirm"
        width="420px"
    >
        <div class="dislike-reasons">
            <div class="reason-label">{{ $t('chat.feedbackSelectReason') }}</div>
            <t-radio-group v-model="selectedReason" class="reason-list">
                <t-radio v-for="reason in reasons" :key="reason.value" :value="reason.value" class="reason-item">
                    {{ reason.label }}
                </t-radio>
                <t-radio value="other" class="reason-item">{{ $t('chat.feedbackReasonOther') }}</t-radio>
            </t-radio-group>
            <t-textarea
                v-if="selectedReason === 'other'"
                v-model="customReason"
                :placeholder="$t('chat.feedbackOtherReasonPlaceholder')"
                :maxlength="200"
                class="custom-reason-input"
            />
        </div>
    </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

const props = defineProps<{
    modelValue: boolean;
    messageId: string;
}>();

const emit = defineEmits<{
    (e: 'update:modelValue', value: boolean): void;
    (e: 'confirm', data: { messageId: string; reason: string }): void;
}>();

const visible = computed({
    get: () => props.modelValue,
    set: (val: boolean) => emit('update:modelValue', val),
});

const reasons = [
    { value: 'not_accurate', label: t('chat.feedbackReasonNotAccurate') },
    { value: 'not_relevant', label: t('chat.feedbackReasonNotRelevant') },
    { value: 'outdated', label: t('chat.feedbackReasonOutdated') },
    { value: 'too_short', label: t('chat.feedbackReasonTooShort') },
    { value: 'too_long', label: t('chat.feedbackReasonTooLong') },
    { value: 'harmful', label: t('chat.feedbackReasonHarmful') },
];

const selectedReason = ref('');
const customReason = ref('');

watch(() => props.modelValue, (val) => {
    if (!val) { selectedReason.value = ''; customReason.value = ''; }
});

const handleConfirm = () => {
    let reason = selectedReason.value;
    if (selectedReason.value === 'other' && customReason.value.trim()) {
        reason = customReason.value.trim();
    }
    if (!reason) reason = t('chat.feedbackReasonOther');
    emit('confirm', { messageId: props.messageId, reason });
    visible.value = false;
};
</script>

<style scoped>
.dislike-reasons { padding: 8px 0; }
.reason-label { margin-bottom: 12px; font-size: 14px; color: var(--td-text-color-secondary); }
.reason-list { display: flex; flex-direction: column; gap: 8px; }
.reason-item { display: block; padding: 6px 0; }
.custom-reason-input { margin-top: 12px; }
</style>
