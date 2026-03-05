<template>
    <div class='deep-think'>
        <div class="think-header" @click="toggleFold">
            <div class="think-title">
                <span v-if="deepSession.thinking" class="thinking-status">
                    <span class="thinking-indicator">
                        <span class="indicator-dot"></span>
                        <span class="indicator-ring"></span>
                    </span>
                    <span class="thinking-text">{{ $t('chat.thinking') }}</span>
                </span>
                <span v-else class="done-status">
                    <img class="done-icon" src="@/assets/img/Frame3718.svg" :alt="$t('chat.deepThoughtAlt')">
                    <span class="done-text">{{ $t('chat.deepThoughtCompleted') }}</span>
                </span>
            </div>
            <div class="toggle-icon-wrapper">
                <t-icon :name="isFold ? 'chevron-down' : 'chevron-up'" class="toggle-icon" />
            </div>
        </div>
        <div class="think-content" v-show="!isFold || deepSession.thinking">
            <div ref="contentInnerRef" class="content-inner" v-html="safeProcessThinkContent(deepSession.thinkContent)"></div>
        </div>
    </div>
</template>
<script setup>
import { watch, ref, defineProps, onMounted, nextTick } from 'vue';
import { sanitizeHTML } from '@/utils/security';
import { useI18n } from 'vue-i18n';

const isFold = ref(false)
const contentInnerRef = ref(null)
const { t } = useI18n()
const props = defineProps({
    // 必填项
    deepSession: {
        type: Object,
        required: false
    }
});

// 初始化时检查：如果 thinking 已完成（从历史记录加载），默认折叠
onMounted(() => {
    if (props.deepSession?.thinking === false) {
        isFold.value = true;
    }
});

// 监听 thinking 状态变化，自动折叠
watch(
    () => props.deepSession?.thinking,
    (newVal, oldVal) => {
        // 当 thinking 从 true 变为 false 时，自动折叠 thinking 内容
        // 只在流式输出场景下触发（oldVal 为 true）
        if (oldVal === true && newVal === false) {
            isFold.value = true;
        }
    }
);

// 监听内容变化，自动滚动到底部
watch(
    () => props.deepSession?.thinkContent,
    () => {
        // 只在 thinking 进行中时滚动
        if (props.deepSession?.thinking) {
            nextTick(() => {
                if (contentInnerRef.value) {
                    contentInnerRef.value.scrollTop = contentInnerRef.value.scrollHeight;
                }
            });
        }
    }
);

const toggleFold = () => {
    // 只有 thinking 完成后才能折叠/展开
    if (!props.deepSession?.thinking) {
        isFold.value = !isFold.value;
    }
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
    display: flex;
    flex-direction: column;
    font-size: 12px;
    width: 100%;
    border-radius: 8px;
    background-color: var(--td-bg-color-container);
    border: .5px solid var(--td-component-stroke);
    box-shadow: 0 2px 4px rgba(7, 192, 95, 0.08);
    overflow: hidden;
    box-sizing: border-box;
    transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
    margin: -8px 0px 10px 0px;

    .think-header {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 6px 14px;
        color: var(--td-text-color-primary);
        font-weight: 500;
        cursor: pointer;
        user-select: none;

        &:hover {
            background-color: rgba(7, 192, 95, 0.04);
        }

        .think-title {
            display: flex;
            align-items: center;
        }

        .thinking-status {
            display: flex;
            align-items: center;

            .thinking-indicator {
                position: relative;
                width: 16px;
                height: 16px;
                margin-right: 8px;
                display: flex;
                align-items: center;
                justify-content: center;

                .indicator-dot {
                    width: 6px;
                    height: 6px;
                    border-radius: 50%;
                    background: var(--td-brand-color);
                    animation: pulse-dot 1.8s ease-in-out infinite;
                }

                .indicator-ring {
                    position: absolute;
                    inset: 0;
                    border-radius: 50%;
                    border: 1.5px solid var(--td-brand-color);
                    opacity: 0;
                    animation: pulse-ring 1.8s ease-out infinite;
                }
            }

            .thinking-text {
                font-size: 12px;
                color: var(--td-text-color-primary);
                white-space: nowrap;
            }
        }

        .done-status {
            display: flex;
            align-items: center;

            .done-icon {
                width: 16px;
                height: 16px;
                margin-right: 8px;
            }

            .done-text {
                font-size: 12px;
                color: var(--td-text-color-primary);
                white-space: nowrap;
            }
        }

        .toggle-icon-wrapper {
            font-size: 14px;
            padding: 0 2px 1px 2px;
            color: var(--td-brand-color);

            .toggle-icon {
                transition: transform 0.2s;
            }
        }
    }

    .think-content {
        border-top: 1px solid var(--td-bg-color-secondarycontainer);

        .content-inner {
            padding: 8px 14px;
            font-size: 12px;
            line-height: 1.6;
            color: var(--td-text-color-secondary);
            max-height: 200px;
            overflow-y: auto;
            word-break: break-word;

            &::-webkit-scrollbar {
                width: 4px;
            }

            &::-webkit-scrollbar-thumb {
                background: rgba(0, 0, 0, 0.1);
                border-radius: 2px;
            }
        }
    }
}

@keyframes pulse-dot {
    0%, 100% {
        transform: scale(0.85);
        opacity: 0.6;
    }
    50% {
        transform: scale(1.1);
        opacity: 1;
    }
}

@keyframes pulse-ring {
    0% {
        transform: scale(0.5);
        opacity: 0.6;
    }
    100% {
        transform: scale(1.2);
        opacity: 0;
    }
}

html[theme-mode="dark"] {
    .deep-think {
        .think-content .content-inner {
            &::-webkit-scrollbar-thumb {
                background: rgba(255, 255, 255, 0.15);
            }
        }
    }
}
</style>
