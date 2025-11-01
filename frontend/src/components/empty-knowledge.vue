<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>
<template>
    <div class="empty">
        <img class="empty-img" src="@/assets/img/upload.svg" alt="">
        <span class="empty-txt">{{ t('knowledgeBase.emptyKnowledgeDragDrop') }}</span>
        <span class="empty-type-txt">{{ t('knowledgeBase.pdfDocFormat') }}</span>
        <span class="empty-type-txt">{{ t('knowledgeBase.textMarkdownFormat') }}</span>
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
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 26px;
    margin: 12px 0 16px 0;
}

.empty-type-txt {
    color: #00000066;
    text-align: center;
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 400;
    width: 217px;
}

.empty-img {
    width: 162px;
    height: 162px;
}
</style>
