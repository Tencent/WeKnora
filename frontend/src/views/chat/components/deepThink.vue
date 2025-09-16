<template>
    <div class='deep-think'>
        <t-collapse :default-value="deepSession.thinking ? [0] : []" expand-icon-placement="right" :expand-icon="true"
            :borderless="true" @change="handlePanelChange">
            <t-collapse-panel>
                <template #expandIcon>
                    <div v-if="!deepSession.thinking" class="chevron">
                        <t-icon :name="!isFold ? 'chevron-up' : 'chevron-down'" />
                    </div>
                </template>
                <template #header>
                    <div class="deep-title">
                        <div v-if="deepSession.thinking" class="thinking">
                            <img class="img_gif" src="@/assets/img/think.gif" alt="">思考中···
                        </div>
                        <div v-else class="done">
                            <img class="icon deep_icon" src="@/assets/img/Frame3718.svg" alt=""></img>已深度思考
                        </div>
                    </div>
                </template>
                <div class="content">
                    <span v-html="safeProcessThinkContent(deepSession.thinkContent)"></span>
                </div>
            </t-collapse-panel>

        </t-collapse>

    </div>
</template>
<script setup>
import { onMounted, watch, computed, ref, reactive, defineProps } from 'vue';
import { sanitizeHTML } from '@/utils/security';

const isFold = ref(true)
const props = defineProps({
    // 必填项
    deepSession: {
        type: Object,
        required: false
    }
});

watch(
    () => props.deepSession,
    (newVal) => { },
    { deep: true }
);
onMounted(() => {
})
const showHide = () => {
    isFold.value = !isFold.value;
}
const handlePanelChange = (val) => {
    isFold.value = !val.length ? true : false;
}

// 安全地处理思考内容，防止XSS攻击
const safeProcessThinkContent = (content) => {
    if (!content || typeof content !== 'string') return '';
    
    // 先处理换行符
    const contentWithBreaks = content.replace(/\n/g, '<br/>');
    
    // 使用DOMPurify进行安全清理，允许基本的文本格式化标签
    const cleanContent = sanitizeHTML(contentWithBreaks);
    
    return cleanContent;
};
</script>
<style lang="less" scoped>
.deep-think {
    background: #30323605;
    border-radius: 2px;
    box-sizing: border-box;

    :deep(.t-collapse.t--border-less .t-collapse-panel__body) {
        background: #30323605 !important;
    }


    :deep(.t-collapse-panel__wrapper .t-collapse-panel__header) {
        background: #30323605 !important;
        padding: 4px;

        &:hover {
            background-color: #30323605 !important;
        }
    }

    :deep(.t-collapse-panel__wrapper .t-collapse-panel__content) {
        padding: 0;
    }

    .chevron {
        color: #00000099;
        font-size: 14px;
        padding: 0 2px 1px 2px;
    }

    .deep-title {
        display: flex;
        justify-content: space-between;
        align-items: center;
        cursor: pointer;
        // padding: 4px;

        &:hover {
            background: #30323605;
        }



        .thinking {
            font-size: 14px;
            color: #000000e6;
            display: flex;
            align-items: center;
        }

        .img_gif {
            width: 16px;
            height: 16px;
            margin-right: 4px;
        }

        .done {
            font-size: 14px;
            color: #00000099;
            display: flex;
            align-items: center;

            .deep_icon {
                width: 16px;
                height: 16px;
                color: #07c05f;
                margin-right: 4px;
            }
        }
    }

    .content {
        line-height: 23px;
        font-size: 14px;
        font-family: "PingFang SC";
        color: #00000099;
        margin: 6px 8px 4px;
        word-break: break-all;
        text-align: justify;
    }
}
</style>