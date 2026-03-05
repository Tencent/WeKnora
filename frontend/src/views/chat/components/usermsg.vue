<template>
    <div class="user_msg_container">
        <!-- 显示@的知识库和文件 -->
        <div v-if="mentioned_items && mentioned_items.length > 0" class="mentioned_items">
            <span 
                v-for="item in mentioned_items" 
                :key="item.id" 
                class="mentioned_tag"
                :class="[
                  item.type === 'kb' ? (item.kb_type === 'faq' ? 'faq-tag' : 'kb-tag') : 'file-tag'
                ]"
            >
                <span class="tag_icon">
                    <t-icon v-if="item.type === 'kb'" :name="item.kb_type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                    <t-icon v-else name="file" />
                </span>
                <span class="tag_name">{{ item.name }}</span>
            </span>
        </div>
        <div class="user_msg">
            {{ content }}
        </div>
    </div>
</template>
<script setup>
import { defineProps } from "vue";

const props = defineProps({
    // 必填项
    content: {
        type: String,
        required: false
    },
    // @提及的知识库和文件
    mentioned_items: {
        type: Array,
        required: false,
        default: () => []
    }
});
</script>
<style scoped lang="less">
.user_msg_container {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 6px;
    width: 100%;
}

.mentioned_items {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    justify-content: flex-end;
    max-width: 100%;
    margin-bottom: 2px;
}

.mentioned_tag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    border-radius: 10px;
    font-size: 12px;
    font-weight: 500;
    max-width: 200px;
    cursor: default;
    transition: all 0.15s;
    background: rgba(7, 192, 95, 0.06);
    border: 1px solid rgba(7, 192, 95, 0.2);
    color: var(--td-text-color-primary);

    &.kb-tag {
        .tag_icon {
            color: var(--td-brand-color);
        }
    }

    &.faq-tag {
        .tag_icon {
            color: var(--td-warning-color);
        }
    }

    &.file-tag {
        .tag_icon {
            color: var(--td-text-color-secondary);
        }
    }

    .tag_icon {
        font-size: 13px;
        display: flex;
        align-items: center;
    }

    .tag_name {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        color: currentColor;
    }
}

.user_msg {
    width: max-content;
    max-width: 776px;
    display: flex;
    padding: 10px 12px;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    gap: 4px;
    flex: 1 0 0;
    border-radius: 4px;
    background: var(--td-success-color);
    margin-left: auto;
    color: var(--td-text-color-primary);
    font-size: 16px;
    text-align: justify;
    word-break: break-all;
    max-width: 100%;
    box-sizing: border-box;
}
</style>
