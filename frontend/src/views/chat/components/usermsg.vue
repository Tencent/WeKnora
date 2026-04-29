<template>
    <div class="user_msg_container" ref="containerRef" :class="{ 'is-embedded': embeddedMode }">
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
        <!-- 显示上传的图片 -->
        <div v-if="hasImages" class="user_images">
            <img 
                v-for="(img, idx) in props.images" 
                :key="idx" 
                :src="img.url" 
                class="user_image_thumb"
                @click="previewImage($event)"
            />
        </div>
        <!-- 显示上传的附件 -->
        <div v-if="hasAttachments" class="user_attachments">
            <div v-for="(att, idx) in props.attachments" :key="idx" class="user_attachment_card">
                <div class="attachment_card_icon">
                    <svg viewBox="0 0 40 48" fill="none" xmlns="http://www.w3.org/2000/svg" width="36" height="44">
                        <rect width="40" height="48" rx="4" fill="#4A90D9"/>
                        <path d="M8 6h16l8 8v28a2 2 0 01-2 2H8a2 2 0 01-2-2V8a2 2 0 012-2z" fill="#5BA3E8"/>
                        <path d="M24 6l8 8h-6a2 2 0 01-2-2V6z" fill="#3A7BC8"/>
                        <rect x="10" y="20" width="20" height="2" rx="1" fill="white" fill-opacity="0.9"/>
                        <rect x="10" y="26" width="20" height="2" rx="1" fill="white" fill-opacity="0.9"/>
                        <rect x="10" y="32" width="14" height="2" rx="1" fill="white" fill-opacity="0.9"/>
                    </svg>
                </div>
                <div class="attachment_card_info">
                    <div class="attachment_card_name">{{ att.file_name }}</div>
                    <div class="attachment_card_meta">{{ getFileExt(att.file_name) }}<span v-if="att.file_size">&nbsp;·&nbsp;{{ formatFileSize(att.file_size) }}</span></div>
                </div>
            </div>
        </div>
        <div class="user_msg">
            {{ content }}
        </div>
        <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg" />
    </div>
</template>
<script setup>
import { defineProps, computed, ref, watch, onMounted, nextTick } from "vue";
import { hydrateProtectedFileImages } from '@/utils/security';
import picturePreview from '@/components/picture-preview.vue';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

const props = defineProps({
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
    }
});

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
    border-radius: 5px;
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

.user_msg_container {
    &.is-embedded {
        .user_msg {
            max-width: 100%;
        }
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
    background: #8CE97F;
    margin-left: auto;
    color: #000000e6;
    font-size: 15px;
    text-align: justify;
    word-break: break-all;
    box-sizing: border-box;
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
    border-radius: 8px;
    border: 1px solid var(--td-border-level-1-color, #e7e7e7);
    background: var(--td-bg-color-container, #fff);
    max-width: 260px;
    min-width: 160px;
    cursor: default;

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
        font-size: 13px;
        font-weight: 500;
        color: var(--td-text-color-primary, #333);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .attachment_card_meta {
        font-size: 11px;
        color: var(--td-text-color-secondary, #999);
        white-space: nowrap;
        box-sizing: border-box;
    }
}

.user_image_thumb {
    width: 120px;
    height: 120px;
    object-fit: cover;
    border-radius: 6px;
    cursor: pointer;
    border: 1px solid var(--td-border-level-2-color, #e7e7e7);
    transition: opacity 0.2s;

    &:hover {
        opacity: 0.85;
    }
}

.channel_tag {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 3px;
    font-size: 11px;
    font-weight: 500;
    line-height: 18px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
    border: 1px solid var(--td-border-level-2-color, #e7e7e7);

    &.channel-web {
        color: var(--td-brand-color);
        background: var(--td-brand-color-light);
        border-color: var(--td-brand-color-2, rgba(0, 82, 217, 0.1));
    }

    &.channel-api {
        color: var(--td-success-color);
        background: var(--td-success-color-1, rgba(0, 168, 112, 0.06));
        border-color: var(--td-success-color-2, rgba(0, 168, 112, 0.15));
    }

    &.channel-im {
        color: var(--td-warning-color);
        background: var(--td-warning-color-1, rgba(237, 123, 0, 0.06));
        border-color: var(--td-warning-color-2, rgba(237, 123, 0, 0.15));
    }
}

html[theme-mode="dark"] {
    .user_msg {
        background: var(--td-brand-color-3);
        color: rgba(255, 255, 255, 0.9);
    }
}
</style>
