<template>
    <div class="refer">
        <div class="refer_header" @click="referBoxSwitch" v-if="session.knowledge_references && session.knowledge_references.length">
            <div class="refer_title">
                <img src="@/assets/img/ziliao.svg" alt="" />
                <span>参考了{{ session.knowledge_references && session.knowledge_references.length }}个相关内容</span>
            </div>
            <div class="refer_show_icon">
                <t-icon :name="showReferBox ? 'chevron-up' : 'chevron-down'" />
            </div>
        </div>
        <div class="refer_box" v-show="showReferBox">
            <div v-for="(item, index) in session.knowledge_references" :key="index">
                <t-popup overlayClassName="refer-to-layer" placement="bottom-left" width="400" :showArrow="false"
                    trigger="click">
                    <template #content>
                        <div class="doc_content">
                            <div v-html="safeProcessContent(item.content)"></div>
                        </div>
                    </template>
                    <span class="doc">
                        {{ session.knowledge_references.length < 2 ? item.knowledge_title : `${index +
                            1}.${item.knowledge_title}` }} </span>
                </t-popup>
            </div>
        </div>
    </div>
</template>
<script setup>
import { onMounted, defineProps, computed, ref, reactive } from "vue";
import { sanitizeHTML } from '@/utils/security';
const props = defineProps({
    // 必填项
    content: {
        type: String,
        required: false
    },
    session: {
        type: Object,
        required: false
    }
});
const showReferBox = ref(false);
const referBoxSwitch = () => {
    showReferBox.value = !showReferBox.value;
};

// 安全地处理内容
const safeProcessContent = (content) => {
    if (!content) return '';
    // 先进行安全清理，然后处理换行
    const sanitized = sanitizeHTML(content);
    return sanitized.replace(/\n/g, '<br/>');
};

</script>
<style lang="less" scoped>
.refer {
    display: flex;
    flex-direction: column;
    font-size: 14px;
    width: 100%;
    border-radius: 2px;
    background-color: #30323605;
    overflow: hidden;
    box-sizing: border-box;

    .refer_header {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 4px;
        color: #00000099;

        .refer_title {
            display: flex;
            align-items: center;

            img {
                width: 16px;
                height: 16px;
                color: #07c05f;
                fill: currentColor;
                margin-right: 6px;
            }

            span {
                white-space: nowrap;
            }
        }

        .refer_show_icon {
            font-size: 14px;
            padding: 0 2px 1px 2px;
        }
    }

    .refer_header:hover {
        border-radius: 2px;
        background-color: #30323605;
        cursor: pointer;
    }

    .refer_box {
        padding: 2px 4px 4px 4px;
        flex-direction: column;
    }
}

.doc_content {
    max-height: 400px;
    overflow: auto;
    font-size: 14px;
    color: #000000e6;
    line-height: 23px;
    text-align: justify;
    border: 1px solid #07c05f33;
    padding: 8px;
}

.doc {
    text-decoration: underline;
    color: #366ef4;
    cursor: pointer;
    display: inline-block;
    white-space: nowrap;
    max-width: calc(100% - 24px);
    overflow: hidden;
    text-overflow: ellipsis;
    line-height: 20px;
}
</style>

<style>
.refer-to-layer {
    width: 400px;
}
</style>