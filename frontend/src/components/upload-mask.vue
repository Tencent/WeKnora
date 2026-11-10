<script setup lang="ts">
import { MAX_FILE_SIZE_MB } from '@/utils'
</script>
<template>
    <div class="mask">
        <img class="upload-mask-img" src="@/assets/img/upload-mask.svg" alt="">
        <span class="drag-txt">{{ $t('file.upload') }}</span>
        <span class="drag-type-txt">{{ $t('knowledgeBase.pdfDocFormat', { size: MAX_FILE_SIZE_MB }) }}</span>
        <span class="drag-type-txt">{{ $t('knowledgeBase.textMarkdownFormat', { size: MAX_FILE_SIZE_MB }) }}</span>
    </div>
</template>
<style scoped lang="less">
.mask{
    display: flex;
    flex-flow: column;
    justify-content: center;
    align-items: center;
}
.drag-txt {
    color: var(--td-brand-color);
    font-family: var(--app-font-family);
    font-size: var(--app-text-4xl);
    font-weight: 600;
    line-height: 26px;
    display: inline-block;
    margin: 12px 0 16px 0;
}

.drag-type-txt {
    width: 217px;
    color: var(--td-text-color-disabled);
    text-align: center;
    font-family: var(--app-font-family);
    font-size: var(--app-text-sm);
    font-weight: 400;
}
.upload-img{
    width: 162px;
    height: 162px;
}
</style>
