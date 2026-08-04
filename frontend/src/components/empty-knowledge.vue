<script setup lang="ts">
import { useI18n } from 'vue-i18n'

withDefaults(defineProps<{
    showActions?: boolean
    showDingtalkImport?: boolean
}>(), {
    showActions: false,
    showDingtalkImport: false,
})

const emit = defineEmits<{
    uploadLocal: []
    importDingtalk: []
}>()

const { t } = useI18n()
</script>
<template>
    <div class="empty">
        <img class="empty-img" src="@/assets/img/upload.svg" alt="">
        <span class="empty-txt">{{ t('knowledgeBase.emptyKnowledgeDragDrop') }}</span>
        <span class="empty-type-txt">{{ t('knowledgeBase.pdfDocFormat') }}</span>
        <span class="empty-type-txt">{{ t('knowledgeBase.textMarkdownFormat') }}</span>
        <div v-if="showActions" class="empty-actions">
            <t-button theme="primary" @click="emit('uploadLocal')">
                {{ t('knowledgeBase.uploadLocalFile') }}
            </t-button>
            <t-button v-if="showDingtalkImport" variant="outline" @click="emit('importDingtalk')">
                {{ t('knowledgeBase.importFromDingtalk') }}
            </t-button>
        </div>
    </div>
</template>
<style scoped lang="less">
.empty {
    flex: 1;
    display: flex;
    flex-flow: column;
    justify-content: center;
    align-items: center;
}

.empty-txt {
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 16px;
    font-weight: 600;
    line-height: 26px;
    margin: 12px 0 16px 0;
}

.empty-type-txt {
    color: var(--td-text-color-disabled);
    text-align: center;
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    width: 217px;
}

.empty-img {
    width: 162px;
    height: 162px;
}

.empty-actions {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 12px;
    margin-top: 20px;
}
</style>
