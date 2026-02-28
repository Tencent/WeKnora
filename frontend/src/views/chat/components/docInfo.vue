<template>
    <div class="refer">
        <div class="refer_header" @click="referBoxSwitch" v-if="session.knowledge_references && session.knowledge_references.length">
            <div class="refer_title">
                <img src="@/assets/img/ziliao.svg" :alt="$t('chat.referenceIconAlt')" />
                <span>{{ headerText }}</span>
            </div>
            <div class="refer_show_icon">
                <t-icon :name="showReferBox ? 'chevron-up' : 'chevron-down'" />
            </div>
        </div>
        <div class="refer_box" v-show="showReferBox">
            <!-- Web search references (ungrouped) -->
            <div v-for="(item, index) in webSearchRefs" :key="'web-' + index">
                <a
                    :href="getWebSearchUrl(item)"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="doc doc-web"
                    @click.stop
                >
                    {{ webSearchRefs.length < 2 ? getWebSearchDisplayText(item) : `${index + 1}. ${getWebSearchDisplayText(item)}` }}
                </a>
            </div>

            <!-- Knowledge references grouped by document -->
            <div v-for="(group, gIdx) in groupedKnowledgeRefs" :key="'grp-' + gIdx" class="doc-group">
                <div class="doc-group-header" @click="toggleGroup(group.key)">
                    <div class="doc-group-left">
                        <t-icon :name="expandedGroups[group.key] ? 'chevron-down' : 'chevron-right'" size="14px" class="doc-group-arrow" />
                        <t-icon name="file" size="14px" class="doc-group-icon" />
                        <span class="doc-group-title" :title="group.title">{{ group.title }}</span>
                        <span class="doc-group-count">{{ $t('chat.referenceChunkCount', { count: group.chunks.length }) }}</span>
                    </div>
                    <div class="doc-group-actions" v-if="group.knowledgeBaseId" @click.stop>
                        <t-tooltip :content="$t('chat.navigateToDocument')">
                            <span class="doc-group-navigate" @click="navigateToDocument(group)">
                                <t-icon name="jump" size="14px" />
                            </span>
                        </t-tooltip>
                    </div>
                </div>
                <div class="doc-group-chunks" v-show="expandedGroups[group.key]">
                    <div v-for="(chunk, cIdx) in group.chunks" :key="'chunk-' + cIdx" class="doc-chunk-item">
                        <t-popup overlayClassName="refer-to-layer" placement="bottom-left" width="400" :showArrow="false" trigger="click">
                            <template #content>
                                <ContentPopup :content="safeProcessContent(chunk.content)" :is-html="true" />
                            </template>
                            <span class="doc-chunk-text">
                                <span class="doc-chunk-index">{{ $t('chat.chunkLabel', { index: cIdx + 1 }) }}</span>
                                {{ truncateContent(chunk.content, 80) }}
                            </span>
                        </t-popup>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>
<script setup>
import { defineProps, computed, ref, reactive } from "vue";
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { sanitizeHTML } from '@/utils/security';
import ContentPopup from './tool-results/ContentPopup.vue';

const router = useRouter();
const { t } = useI18n();

const props = defineProps({
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
const expandedGroups = reactive({});

const referBoxSwitch = () => {
    showReferBox.value = !showReferBox.value;
};

const toggleGroup = (key) => {
    expandedGroups[key] = !expandedGroups[key];
};

const webSearchRefs = computed(() => {
    if (!props.session?.knowledge_references) return [];
    return props.session.knowledge_references.filter(item => item.chunk_type === 'web_search');
});

const knowledgeRefs = computed(() => {
    if (!props.session?.knowledge_references) return [];
    return props.session.knowledge_references.filter(item => item.chunk_type !== 'web_search');
});

const groupedKnowledgeRefs = computed(() => {
    const refs = knowledgeRefs.value;
    if (!refs.length) return [];

    const groupMap = new Map();
    for (const item of refs) {
        const key = item.knowledge_id || item.knowledge_title || item.id;
        if (!groupMap.has(key)) {
            groupMap.set(key, {
                key,
                title: item.knowledge_title || item.knowledge_filename || key,
                knowledgeId: item.knowledge_id,
                knowledgeBaseId: item.knowledge_base_id,
                chunks: [],
            });
        }
        groupMap.get(key).chunks.push(item);
    }
    return Array.from(groupMap.values());
});

const headerText = computed(() => {
    const total = props.session?.knowledge_references?.length ?? 0;
    const docCount = groupedKnowledgeRefs.value.length;
    const webCount = webSearchRefs.value.length;
    if (docCount > 0 && webCount > 0) {
        return t('chat.referencesDocAndWebCount', { docCount, webCount });
    }
    if (docCount > 0) {
        return t('chat.referencesDocCount', { count: docCount });
    }
    return t('chat.referencesTitle', { count: total });
});

const safeProcessContent = (content) => {
    if (!content) return '';
    const sanitized = sanitizeHTML(content);
    return sanitized.replace(/\n/g, '<br/>');
};

const truncateContent = (content, maxLen) => {
    if (!content) return '';
    const text = content.replace(/\n/g, ' ').trim();
    if (text.length <= maxLen) return text;
    return text.slice(0, maxLen) + '...';
};

const navigateToDocument = (group) => {
    if (!group.knowledgeBaseId) return;
    const query = {};
    if (group.knowledgeId) {
        query.knowledge_id = group.knowledgeId;
    }
    router.push({
        path: `/platform/knowledge-bases/${group.knowledgeBaseId}`,
        query
    });
};

const getWebSearchUrl = (item) => {
    if (item.metadata?.url) {
        return item.metadata.url;
    }
    if (item.id && (item.id.startsWith('http://') || item.id.startsWith('https://'))) {
        return item.id;
    }
    return '#';
};

const getWebSearchDisplayText = (item) => {
    if (item.knowledge_title) {
        return item.knowledge_title;
    }
    if (item.metadata?.title) {
        return item.metadata.title;
    }
    const url = getWebSearchUrl(item);
    if (url && url !== '#') {
        try {
            const urlObj = new URL(url);
            return urlObj.hostname;
        } catch {
            return url;
        }
    }
    return 'Web Search Result';
};
</script>
<style lang="less" scoped>
.refer {
    display: flex;
    flex-direction: column;
    font-size: 12px;
    width: 100%;
    border-radius: 8px;
    background-color: #ffffff;
    box-shadow: 0 2px 4px rgba(7, 192, 95, 0.08);
    overflow: hidden;
    box-sizing: border-box;
    transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
    margin-bottom: 8px;

    .refer_header {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 6px 14px;
        color: #333333;
        font-weight: 500;

        .refer_title {
            display: flex;
            align-items: center;

            img {
                width: 16px;
                height: 16px;
                color: #07c05f;
                fill: currentColor;
                margin-right: 8px;
            }

            span {
                white-space: nowrap;
                font-size: 12px;
            }
        }

        .refer_show_icon {
            font-size: 14px;
            padding: 0 2px 1px 2px;
            color: #07c05f;
        }
    }

    .refer_header:hover {
        background-color: rgba(7, 192, 95, 0.04);
        cursor: pointer;
    }

    .refer_box {
        padding: 4px 14px 8px 14px;
        flex-direction: column;
        border-top: 1px solid #f0f0f0;
    }
}

.doc {
    text-decoration: none;
    color: #07c05f;
    cursor: pointer;
    display: inline-block;
    white-space: nowrap;
    max-width: calc(100% - 24px);
    overflow: hidden;
    text-overflow: ellipsis;
    line-height: 20px;
    padding: 2px 0;
    transition: all 0.2s ease;
    border-bottom: 1px solid transparent;

    &:hover {
        border-bottom-color: #07c05f;
    }

    &.doc-web {
        white-space: normal;
        word-break: break-all;

        &:hover {
            text-decoration: underline;
        }
    }
}

.doc-group {
    margin-top: 4px;

    .doc-group-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        padding: 4px 4px;
        border-radius: 4px;
        cursor: pointer;
        transition: background-color 0.15s ease;

        &:hover {
            background-color: rgba(7, 192, 95, 0.04);
        }

        .doc-group-left {
            display: flex;
            align-items: center;
            min-width: 0;
            flex: 1;
        }

        .doc-group-arrow {
            color: #999;
            flex-shrink: 0;
            margin-right: 2px;
        }

        .doc-group-icon {
            color: #07c05f;
            flex-shrink: 0;
            margin-right: 6px;
        }

        .doc-group-title {
            color: #333;
            font-weight: 500;
            white-space: nowrap;
            overflow: hidden;
            text-overflow: ellipsis;
            max-width: 200px;
        }

        .doc-group-count {
            color: #999;
            font-size: 11px;
            margin-left: 6px;
            white-space: nowrap;
            flex-shrink: 0;
        }

        .doc-group-actions {
            flex-shrink: 0;
            margin-left: 8px;
        }

        .doc-group-navigate {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            width: 22px;
            height: 22px;
            border-radius: 4px;
            color: #07c05f;
            cursor: pointer;
            transition: all 0.15s ease;

            &:hover {
                background-color: rgba(7, 192, 95, 0.1);
            }
        }
    }

    .doc-group-chunks {
        padding-left: 22px;
    }
}

.doc-chunk-item {
    .doc-chunk-text {
        display: block;
        color: #666;
        font-size: 12px;
        line-height: 18px;
        padding: 3px 6px;
        border-radius: 4px;
        cursor: pointer;
        transition: background-color 0.15s ease;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;

        &:hover {
            background-color: rgba(7, 192, 95, 0.04);
            color: #07c05f;
        }

        .doc-chunk-index {
            color: #999;
            font-size: 11px;
            margin-right: 4px;
        }
    }
}
</style>

<style>
.refer-to-layer {
    width: 400px;
    max-width: 500px;

    .t-popup__content {
        max-height: 400px;
        max-width: 500px;
        overflow-y: auto;
        overflow-x: hidden;
        word-wrap: break-word;
        word-break: break-word;
    }
}
</style>
