<template>
    <div class="user_msg_container" ref="containerRef" :class="{ 'is-embedded': embeddedMode }">
        <!-- 显示@的知识库和文件 -->
        <div v-if="mentioned_items && mentioned_items.length > 0" class="mentioned_items">
            <span v-for="item in mentioned_items" :key="item.id" class="mentioned_tag" :class="[
                mentionTagClass(item)
            ]">
                <span class="tag_icon">
                    <t-icon v-if="item.type === 'kb'" :name="item.kb_type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                    <t-icon v-else :name="mentionTagIcon(item)" />
                </span>
                <span class="tag_name">{{ item.name }}</span>
            </span>
        </div>
        <!-- 显示上传的图片 -->
        <div v-if="hasImages" class="user_images">
            <img v-for="(img, idx) in props.images" :key="idx" :src="img.url" class="user_image_thumb"
                @click="previewImage($event)" />
        </div>
        <!-- 显示上传的附件 -->
        <div v-if="hasAttachments" class="user_attachments">
            <div v-for="(att, idx) in props.attachments" :key="idx"
                class="user_attachment_card"
                :class="{ 'is-previewable': canPreviewAttachment(att) }"
                @click="openAttachmentPreview(att)">
                <div class="attachment_card_icon">
                    <svg viewBox="0 0 40 48" fill="none" xmlns="http://www.w3.org/2000/svg" width="36" height="44">
                        <rect width="40" height="48" rx="4" fill="#4A90D9" />
                        <path d="M8 6h16l8 8v28a2 2 0 01-2 2H8a2 2 0 01-2-2V8a2 2 0 012-2z" fill="#5BA3E8" />
                        <path d="M24 6l8 8h-6a2 2 0 01-2-2V6z" fill="#3A7BC8" />
                        <rect x="10" y="20" width="20" height="2" rx="1" fill="white" fill-opacity="0.9" />
                        <rect x="10" y="26" width="20" height="2" rx="1" fill="white" fill-opacity="0.9" />
                        <rect x="10" y="32" width="14" height="2" rx="1" fill="white" fill-opacity="0.9" />
                    </svg>
                </div>
                <div class="attachment_card_info">
                    <div class="attachment_card_name">{{ att.file_name }}</div>
                    <div class="attachment_card_meta">{{ getFileExt(att.file_name) }}<span
                            v-if="att.file_size">&nbsp;·&nbsp;{{ formatFileSize(att.file_size) }}</span></div>
                </div>
            </div>
        </div>
        <div class="user_msg">
            {{ content }}
        </div>
        <div v-if="timestamp || content || canFork || canRewind" class="user_msg_meta">
            <time v-if="timestamp" class="user_msg_time" :datetime="timestamp.datetime" :title="fullTimestamp">
                {{ timestamp.time }}
            </time>
            <div v-if="content || canFork || canRewind" class="user_msg_actions">
                <t-tooltip v-if="content" :content="t('agent.copy')">
                    <button type="button" class="user_msg_action" :aria-label="t('agent.copy')" @click="handleCopy">
                        <t-icon name="copy" />
                    </button>
                </t-tooltip>
                <t-tooltip v-if="canFork" :content="forkTooltip">
                    <button type="button" class="user_msg_action" :aria-label="forkTooltip" @click="emit('fork', messageId)">
                        <t-icon name="git-branch" />
                    </button>
                </t-tooltip>
                <t-popconfirm
                    v-if="canRewind"
                    :content="t('chat.rewind.confirmBody')"
                    :confirm-btn="{ content: t('chat.rewind.confirmButton'), theme: 'danger' }"
                    :cancel-btn="{ content: t('chat.rewind.cancelButton') }"
                    theme="warning"
                    placement="top"
                    overlay-class-name="chat-rewind-popconfirm"
                    @confirm="emit('rewind', messageId)"
                >
                    <t-tooltip :content="rewindTooltip">
                        <button type="button" class="user_msg_action" :aria-label="rewindTooltip" @click.stop>
                            <t-icon name="rollback" />
                        </button>
                    </t-tooltip>
                </t-popconfirm>
            </div>
        </div>
        <div v-if="steerFailed" class="steer-failure" role="status">
            <span>{{ t('input.messages.steerFailed') }}</span>
            <t-tooltip :content="t('input.steerRetry')"><button type="button" :aria-label="t('input.steerRetry')" @click="emit('retry-steer')"><t-icon name="refresh" /></button></t-tooltip>
            <t-tooltip :content="t('common.remove')"><button type="button" :aria-label="t('common.remove')" @click="emit('remove-steer')"><t-icon name="close" /></button></t-tooltip>
        </div>
        <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg" />
    </div>
</template>
<script setup>
import { computed, ref, watch, onMounted, nextTick } from "vue";
import { hydrateProtectedFileImages } from '@/utils/security';
import picturePreview from '@/components/picture-preview.vue';
import { useI18n } from 'vue-i18n';
import { useChatAttachmentPreviewDrawer } from '@/composables/useChatAttachmentPreviewDrawer';
import { isPreviewableAttachment, resolveAttachmentFileType } from '@/utils/attachmentPreview';
import { SKILL_ICON } from '@/types/mention';
import { copyWithToast } from '@/utils/clipboard';
import { formatMessageTimestamp, getConversationTimestampModel } from '@/utils/messageTimestamp';
const emit = defineEmits(['retry-steer', 'remove-steer', 'fork', 'rewind']);

const { t } = useI18n();

const mentionTagClass = (item) => {
    if (item.type === 'kb') return item.kb_type === 'faq' ? 'faq-tag' : 'kb-tag';
    return `${item.type || 'file'}-tag`;
};

const mentionTagIcon = (item) => {
    if (item.type === 'tag') return 'tag';
    if (item.type === 'mcp') return 'tools';
    if (item.type === 'skill') return SKILL_ICON;
    return 'file';
};

const props = defineProps({
    steerFailed: { type: Boolean, default: false },
    content: {
        type: String,
        required: false
    },
    mentioned_items: {
        type: Array,
        required: false,
        default: () => []
    },
    images: {
        type: Array,
        required: false,
        default: () => []
    },
    attachments: {
        type: Array,
        required: false,
        default: () => []
    },
    channel: {
        type: String,
        required: false,
        default: ''
    },
    embeddedMode: {
        type: Boolean,
        default: false
    },
    sessionId: {
        type: String,
        default: ''
    },
    messageId: {
        type: String,
        default: ''
    },
    createdAt: {
        type: String,
        default: ''
    },
    canFork: {
        type: Boolean,
        default: false
    },
    canRewind: {
        type: Boolean,
        default: false
    }
});

const canFork = computed(() => props.canFork === true && !props.embeddedMode);
const canRewind = computed(() => props.canRewind === true && !props.embeddedMode);
const forkTooltip = '从这里分叉出新会话';
const timestamp = computed(() => getConversationTimestampModel(props.createdAt));
const fullTimestamp = computed(() => formatMessageTimestamp(props.createdAt));
const handleCopy = () => copyWithToast(props.content, 'common.copySuccess', 'common.copyFailed');
const rewindTooltip = computed(() => t('chat.rewind.tooltip'));

const attachmentPreviewDrawer = useChatAttachmentPreviewDrawer();

const channelLabelMap = {
    web: () => t('chat.channelWeb'),
    api: () => t('chat.channelApi'),
    im: () => t('chat.channelIm'),
};

const channelLabel = computed(() => {
    if (!props.channel) return '';
    const label = channelLabelMap[props.channel];
    return typeof label === 'function' ? label() : (label || props.channel);
});

const channelClass = computed(() => props.channel ? `channel-${props.channel}` : '');

const containerRef = ref(null);
const hasImages = computed(() => props.images && props.images.length > 0);
const hasAttachments = computed(() => props.attachments && props.attachments.length > 0);

const getAttachmentIcon = (fileNameOrType) => {
    const ext = (fileNameOrType || '').split('.').pop()?.toLowerCase();
    if (['pdf'].includes(ext)) return 'file-pdf';
    if (['doc', 'docx'].includes(ext)) return 'file-word';
    if (['xls', 'xlsx'].includes(ext)) return 'file-excel';
    if (['ppt', 'pptx'].includes(ext)) return 'file-powerpoint';
    if (['txt', 'md'].includes(ext)) return 'file';
    if (['mp3', 'wav', 'm4a', 'flac', 'ogg', 'aac'].includes(ext)) return 'sound';
    return 'file';
};

const getFileExt = (fileName) => {
    return (fileName || '').split('.').pop()?.toUpperCase() || 'FILE';
};

const formatFileSize = (bytes) => {
    if (!bytes) return '';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
};

const canPreviewAttachment = (attachment) => {
    return Boolean(props.sessionId) && isPreviewableAttachment(attachment);
};

const openAttachmentPreview = (attachment) => {
    if (!canPreviewAttachment(attachment) || !attachmentPreviewDrawer) return;
    attachmentPreviewDrawer.open({
        sessionId: props.sessionId,
        attachmentId: attachment.id,
        fileName: attachment.file_name,
        fileType: resolveAttachmentFileType(attachment.file_name, attachment.file_type),
    });
};

const hydrateImages = async () => {
    await nextTick();
    await hydrateProtectedFileImages(containerRef.value);
};

watch(() => props.images, hydrateImages);
onMounted(hydrateImages);

const reviewImg = ref(false);
const reviewUrl = ref('');

const previewImage = (event) => {
    const src = event.target?.src;
    if (src) {
        reviewUrl.value = src;
        reviewImg.value = true;
    }
};

const closePreImg = () => {
    reviewImg.value = false;
    reviewUrl.value = '';
};
</script>
<style scoped lang="less">
@import '../../../components/css/chat-resource-chips.less';

.user_msg_container {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 6px;
    width: 100%;
}

.mentioned_items {
    .chat-mentioned-items(flex-end);
}

.mentioned_tag {
    .chat-mentioned-tag();
}

.user_msg_container {
    &.is-embedded {
        .user_msg {
            max-width: 100%;
        }
    }
}

.user_msg {
    width: max-content;
    max-width: min(76%, 820px);
    display: flex;
    padding: 8px 12px;
    flex-direction: column;
    justify-content: center;
    align-items: flex-start;
    gap: 4px;
    flex: 1 0 0;
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-secondarycontainer);
    margin-left: auto;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-xl);
    line-height: 1.6;
    text-align: left;
    word-break: break-word;
    overflow-wrap: anywhere;
    box-sizing: border-box;
    white-space: pre-wrap;
}

.user_images {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    justify-content: flex-end;
    max-width: 100%;
}

.user_attachments {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    justify-content: flex-end;
    max-width: 100%;
}

.user_attachment_card {
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 10px;
    padding: 8px 12px;
    border-radius: var(--app-radius-md);
    border: 1px solid var(--td-border-level-1-color);
    background: var(--td-bg-color-container);
    max-width: 260px;
    min-width: 160px;
    cursor: default;

    &.is-previewable {
        cursor: pointer;
        transition: border-color var(--app-motion-base), box-shadow var(--app-motion-base);

        &:hover {
            border-color: var(--td-brand-color-2);
            box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
        }
    }

    .attachment_card_icon {
        flex-shrink: 0;
        display: flex;
        align-items: center;
        justify-content: center;
    }

    .attachment_card_info {
        flex: 1;
        min-width: 0;
        display: flex;
        flex-direction: column;
        gap: 2px;
    }

    .attachment_card_name {
        font-size: var(--app-text-md);
        font-weight: 500;
        color: var(--td-text-color-primary);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .attachment_card_meta {
        font-size: var(--app-text-xs);
        color: var(--td-text-color-secondary);
        white-space: nowrap;
        box-sizing: border-box;
    }
}

.user_image_thumb {
    width: 120px;
    height: 120px;
    object-fit: cover;
    border-radius: var(--app-radius-sm);
    cursor: pointer;
    border: 1px solid var(--td-border-level-2-color);
    transition: opacity var(--app-motion-base);

    &:hover {
        opacity: 0.85;
    }
}

.channel_tag {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 3px;
    font-size: var(--app-text-xs);
    font-weight: 500;
    line-height: 18px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
    border: 1px solid var(--td-border-level-2-color);

    &.channel-web {
        color: var(--td-brand-color);
        background: var(--td-brand-color-light);
        border-color: var(--td-brand-color-2);
    }

    &.channel-api {
        color: var(--td-success-color);
        background: var(--td-success-color-1);
        border-color: var(--td-success-color-2);
    }

    &.channel-im {
        color: var(--td-warning-color);
        background: var(--td-warning-color-1);
        border-color: var(--td-warning-color-2);
    }
}

html[theme-mode="dark"] {
    .user_msg {
        background: var(--td-bg-color-secondarycontainer);
        color: var(--td-text-color-primary);
    }
}
</style>

<style scoped>
.steer-failure { display: flex; align-items: center; justify-content: flex-end; gap: 4px; font-size: var(--app-text-sm); color: var(--td-text-color-secondary); margin-bottom: 4px; }
.steer-failure { margin-top: 6px; color: var(--td-error-color); }
.steer-failure button { display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; border: 0; border-radius: var(--app-radius-sm); background: transparent; color: inherit; cursor: pointer; }
.steer-failure button:hover { background: var(--td-bg-color-secondarycontainer); }

.user_msg_meta {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    min-height: 28px;
    padding-right: 4px;
    color: var(--td-text-color-placeholder);
    opacity: 0;
    pointer-events: none;
    transition: opacity 140ms ease;
}

.user_msg_time {
    font-size: var(--app-text-sm);
    font-variant-numeric: tabular-nums;
    line-height: 20px;
}

.user_msg_actions {
    display: flex;
    align-items: center;
    gap: 2px;
}

.user_msg_container:hover .user_msg_meta,
.user_msg_container:focus-within .user_msg_meta {
    opacity: 1;
    pointer-events: auto;
}

.user_msg_action {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: 0;
    border-radius: var(--app-radius-sm);
    background: transparent;
    color: inherit;
    font-size: var(--app-text-xl);
    cursor: pointer;
}

.user_msg_action:hover {
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-container-hover);
}

.user_msg_action:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 2px;
}

@media (hover: none) {
    .user_msg_meta {
        opacity: 1;
        pointer-events: auto;
    }
}

@media (prefers-reduced-motion: reduce) {
    .user_msg_meta {
        transition: none;
    }
}
</style>
