<template>
    <!--
        ChatArtifactsDrawer — right-side drawer that lists every skill-generated
        file attached to the surrounding assistant message, and opens an inline
        preview that reuses DocumentPreview (same renderer as knowledge files
        and chat attachments).

        Usage (from botmsg.vue / AgentStreamDisplay.vue):
            <ChatArtifactsDrawer
                v-model:visible="showArtifacts"
                :session-id="sessionId"
                :message-id="messageId"
                :artifacts="artifacts"
            />
    -->
    <teleport to="body">
        <div
            v-if="previewResizing"
            class="artifact-preview-resize-overlay"
            aria-hidden="true"
        />
        <div
            v-if="internalVisible && previewItem"
            class="artifact-preview-resize-handle"
            :class="{ 'artifact-preview-resize-handle--active': previewResizing }"
            :style="{ right: `${previewWidth}px` }"
            role="separator"
            aria-orientation="vertical"
            @mousedown.prevent="onPreviewResizeStart"
        >
            <div class="artifact-preview-resize-line" />
        </div>
    </teleport>

    <t-drawer
        v-model:visible="internalVisible"
        class="chat-artifacts-drawer"
        :class="{ 'chat-artifacts-drawer--preview': !!previewItem, 'chat-artifacts-drawer--resizing': previewResizing }"
        placement="right"
        :size="drawerSize"
        :z-index="previewItem ? 2000 : 1500"
        attach="body"
        :footer="false"
        :close-on-overlay-click="true"
        :close-on-esc-keydown="true"
        :destroy-on-close="false"
        @close="handleClose"
    >
        <template #header>
            <div v-if="previewItem" class="artifact-drawer-header artifact-drawer-header--preview">
                <t-button
                    class="artifact-back"
                    variant="text"
                    shape="square"
                    size="small"
                    :title="$t('agent.artifactDrawer.previewBack')"
                    :aria-label="$t('agent.artifactDrawer.previewBack')"
                    @click="closePreview"
                >
                    <template #icon>
                        <t-icon name="chevron-left" size="18px" />
                    </template>
                </t-button>
                <div class="artifact-drawer-header-icon">
                    <t-icon :name="getFileIcon(previewItem.file_name)" />
                </div>
                <div class="artifact-drawer-header-title" :title="previewItem.file_name">{{ previewItem.file_name }}</div>
                <t-button
                    class="artifact-download"
                    variant="text"
                    shape="square"
                    size="small"
                    :title="$t('agent.artifactDrawer.download')"
                    :loading="!!downloading[previewItem.index]"
                    @click="handleDownload(previewItem)"
                >
                    <template #icon>
                        <t-icon name="download" size="16px" />
                    </template>
                </t-button>
            </div>
            <div v-else class="artifact-drawer-header">
                <div class="artifact-drawer-header-icon">
                    <t-icon name="folder" />
                </div>
                <div class="artifact-drawer-header-title">{{ $t('agent.artifactDrawer.title') }}</div>
            </div>
        </template>
        <div v-if="previewItem" class="artifact-preview-body">
            <DocumentPreview
                :session-id="sessionId"
                :message-id="messageId"
                :artifact-index="previewItem.index"
                :file-type="previewFileType"
                :file-name="previewItem.file_name"
                :active="internalVisible"
                fill-height
            />
        </div>
        <div v-else-if="loading" class="artifact-drawer-empty">
            <t-loading size="small" />
            <span>{{ $t('common.loading') }}</span>
        </div>
        <div v-else-if="!items.length" class="artifact-drawer-empty">
            <t-icon name="folder-open" size="32px" />
            <span>{{ $t('agent.artifactDrawer.empty') }}</span>
        </div>
        <ul v-else class="artifact-list">
            <li
                v-for="item in items"
                :key="`${item.index}-${item.file_name}`"
                class="artifact-item is-previewable"
                @click="openPreview(item)"
            >
                <span class="artifact-icon">
                    <t-icon :name="getFileIcon(item.file_name)" />
                </span>
                <div class="artifact-body">
                    <div class="artifact-name" :title="item.file_name">{{ item.file_name }}</div>
                    <div class="artifact-meta">
                        <span>{{ formatFileSize(item.file_size) }}</span>
                        <span class="artifact-meta-sep">·</span>
                        <span>{{ formatDateTime(item.created_at) }}</span>
                    </div>
                </div>
                <t-button
                    class="artifact-preview-btn"
                    variant="text"
                    shape="square"
                    size="small"
                    :title="$t('agent.artifactDrawer.preview')"
                    @click.stop="openPreview(item)"
                >
                    <template #icon>
                        <t-icon name="browse" size="16px" />
                    </template>
                </t-button>
                <t-button
                    class="artifact-download"
                    variant="text"
                    shape="square"
                    size="small"
                    :title="$t('agent.artifactDrawer.download')"
                    :loading="!!downloading[item.index]"
                    @click.stop="handleDownload(item)"
                >
                    <template #icon>
                        <t-icon name="download" size="16px" />
                    </template>
                </t-button>
            </li>
        </ul>
    </t-drawer>
</template>

<script setup lang="ts">
/*
 * Design notes:
 *   - The drawer is stateless w.r.t. the download itself; it delegates to
 *     the chat API's downloadArtifact() helper which uses the axios blob
 *     transport (getDown) so the Bearer token stays attached — plain
 *     <a href> would drop it and hit 401.
 *   - Preview reuses DocumentPreview so HTML charts, CSV tables, PDFs,
 *     images, office docs, audio/video, mermaid, and unlabeled text all
 *     go through the same renderer as knowledge-file / attachment preview.
 *   - `items` prefers the props-provided list (already in memory from the
 *     stream payload). When empty, we pull from /artifacts on open so a
 *     refreshed page still shows something without waiting for the parent
 *     to re-hydrate.
 *   - Errors during download are surfaced via MessagePlugin.error but do
 *     NOT close the drawer, matching spec §7: "抽屉保持打开以便重试其他文件".
 *   - Overlay / Esc while a file is previewed pop back to the list (same as
 *     the header chevron). TDesign still emits update:visible=false after
 *     @close, so we swallow that one emit; the header X still closes all.
 */
import { computed, onMounted, onUnmounted, ref, watch, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { downloadArtifact, listMessageArtifacts, type ArtifactMeta } from '@/api/chat'
import { getFileIcon } from '@/utils/files'
import { resolveFilePreviewExt } from '@/utils/filePreview'
import DocumentPreview from '@/components/document-preview.vue'

const LIST_WIDTH = 440
const PREVIEW_WIDTH_KEY = 'weknora-chat-artifact-preview-width'
const PREVIEW_DEFAULT_WIDTH = 760
const PREVIEW_MIN_WIDTH = 520

const props = defineProps<{
    visible: boolean
    sessionId: string
    messageId: string
    artifacts?: ArtifactMeta[]
}>()

const emit = defineEmits<{
    (e: 'update:visible', v: boolean): void
}>()

const { t } = useI18n()

/** TDesign always follows @close with update:visible=false; swallow that when popping preview. */
let suppressDrawerClose = false

const internalVisible = computed({
    get: () => props.visible,
    set: (v: boolean) => {
        if (!v && suppressDrawerClose) {
            suppressDrawerClose = false
            return
        }
        emit('update:visible', v)
    },
})

const loading = ref(false)
const fetched = ref<ArtifactMeta[]>([])
const downloading = reactive<Record<number, boolean>>({})
const previewItem = ref<ArtifactMeta | null>(null)
const previewWidth = ref(PREVIEW_DEFAULT_WIDTH)
const previewResizing = ref(false)

let previewResizeStartX = 0
let previewResizeStartWidth = 0

const items = computed<ArtifactMeta[]>(() => {
    if (props.artifacts && props.artifacts.length) return props.artifacts
    return fetched.value
})

const previewFileType = computed(() => {
    const item = previewItem.value
    if (!item) return ''
    return resolveFilePreviewExt(item.file_name, item.file_type)
})

const drawerSize = computed(() => (
    previewItem.value ? `${previewWidth.value}px` : `${LIST_WIDTH}px`
))

function previewMaxWidth() {
    return Math.min(1600, Math.max(PREVIEW_MIN_WIDTH, Math.floor(window.innerWidth * 0.95)))
}

function clampPreviewWidth(width: number) {
    return Math.max(PREVIEW_MIN_WIDTH, Math.min(previewMaxWidth(), width))
}

function loadPreviewWidth() {
    try {
        const raw = localStorage.getItem(PREVIEW_WIDTH_KEY)
        const parsed = raw ? parseInt(raw, 10) : NaN
        if (!Number.isNaN(parsed)) {
            previewWidth.value = clampPreviewWidth(parsed)
        }
    } catch {
        /* ignore */
    }
}

function onPreviewResizeStart(e: MouseEvent) {
    previewResizing.value = true
    previewResizeStartX = e.clientX
    previewResizeStartWidth = previewWidth.value
    document.addEventListener('mousemove', onPreviewResizeMove)
    document.addEventListener('mouseup', onPreviewResizeEnd)
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
}

function onPreviewResizeMove(e: MouseEvent) {
    const delta = previewResizeStartX - e.clientX
    previewWidth.value = clampPreviewWidth(previewResizeStartWidth + delta)
}

function onPreviewResizeEnd() {
    document.removeEventListener('mousemove', onPreviewResizeMove)
    document.removeEventListener('mouseup', onPreviewResizeEnd)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    previewResizing.value = false
    try {
        localStorage.setItem(PREVIEW_WIDTH_KEY, String(previewWidth.value))
    } catch {
        /* ignore */
    }
}

function cleanupPreviewResize() {
    document.removeEventListener('mousemove', onPreviewResizeMove)
    document.removeEventListener('mouseup', onPreviewResizeEnd)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    previewResizing.value = false
}

function onWindowResize() {
    previewWidth.value = clampPreviewWidth(previewWidth.value)
}

watch(
    () => props.visible,
    async (open) => {
        if (!open) {
            suppressDrawerClose = false
            previewItem.value = null
            return
        }
        if (props.artifacts && props.artifacts.length) {
            fetched.value = []
            return
        }
        if (!props.sessionId || !props.messageId) return
        loading.value = true
        try {
            const res: any = await listMessageArtifacts(props.sessionId, props.messageId)
            const data = (res && (res.data || res)) as ArtifactMeta[] | undefined
            fetched.value = Array.isArray(data) ? data : []
        } catch (err) {
            console.error('[ChatArtifactsDrawer] load failed:', err)
            fetched.value = []
        } finally {
            loading.value = false
        }
    },
)

function handleClose(context?: { trigger?: string }) {
    const popPreview = !!previewItem.value && (context?.trigger === 'overlay' || context?.trigger === 'esc')
    if (popPreview) {
        closePreview()
        suppressDrawerClose = true
        return
    }
    previewItem.value = null
    emit('update:visible', false)
}

function openPreview(item: ArtifactMeta) {
    previewItem.value = item
}

function closePreview() {
    previewItem.value = null
}

function formatFileSize(size: number): string {
    if (!size || size < 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB']
    let s = size
    let unit = 0
    while (s >= 1024 && unit < units.length - 1) {
        s /= 1024
        unit++
    }
    return unit === 0 ? `${s} ${units[unit]}` : `${s.toFixed(1)} ${units[unit]}`
}

function formatDateTime(raw: string): string {
    if (!raw) return '—'
    const d = new Date(raw)
    if (Number.isNaN(d.getTime())) return raw
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function handleDownload(item: ArtifactMeta) {
    if (!props.sessionId || !props.messageId) {
        MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
        return
    }
    downloading[item.index] = true
    try {
        const blob = await downloadArtifact(props.sessionId, props.messageId, item.index)
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = item.file_name || 'artifact'
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (err) {
        console.error('[ChatArtifactsDrawer] download failed:', err)
        MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
    } finally {
        downloading[item.index] = false
    }
}

onMounted(() => {
    loadPreviewWidth()
    window.addEventListener('resize', onWindowResize)
})

onUnmounted(() => {
    window.removeEventListener('resize', onWindowResize)
    cleanupPreviewResize()
})
</script>

<style lang="less" scoped>
.artifact-drawer-header {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    width: 100%;
    padding-right: 32px;
}

.artifact-drawer-header--preview {
    padding-right: 8px;
}

.artifact-back {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);

    :deep(.t-button__icon) {
        margin: 0;
    }
}

.artifact-drawer-header-icon {
    flex-shrink: 0;
    width: 32px;
    height: 32px;
    border-radius: 9px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-brand-color);
    font-size: 16px;
}

.artifact-drawer-header-title {
    min-width: 0;
    flex: 1;
    font-size: 15px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.artifact-preview-body {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    height: 100%;
}

.artifact-drawer-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 48px 16px;
    color: var(--td-text-color-placeholder);
    font-size: 13px;
}

.artifact-list {
    margin: 0;
    padding: 0;
    list-style: none;
}

.artifact-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 4px;
    border-bottom: 1px solid var(--td-component-stroke);
    border-radius: 8px;

    &:last-child {
        border-bottom: none;
    }

    &.is-previewable {
        cursor: pointer;
    }

    &:hover {
        background: var(--td-bg-color-container-hover);
    }

    &:hover .artifact-icon {
        color: var(--td-brand-color);
    }
}

.artifact-icon {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    border-radius: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 16px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    transition: color 0.15s ease;
}

.artifact-body {
    flex: 1;
    min-width: 0;
}

.artifact-name {
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.01em;
    line-height: 1.35;
    color: var(--td-text-color-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}

.artifact-meta {
    margin-top: 2px;
    font-size: 12px;
    line-height: 1.3;
    color: var(--td-text-color-placeholder);
    display: flex;
    align-items: center;
    gap: 4px;
}

.artifact-meta-sep {
    opacity: 0.6;
}

.artifact-preview-btn,
.artifact-download {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);

    :deep(.t-button__icon) {
        margin: 0;
    }
}

.artifact-preview-resize-overlay {
    position: fixed;
    inset: 0;
    z-index: 2001;
    cursor: col-resize;
}

.artifact-preview-resize-handle {
    position: fixed;
    top: 0;
    bottom: 0;
    width: 12px;
    margin-left: -6px;
    z-index: 2002;
    cursor: col-resize;
    display: flex;
    align-items: center;
    justify-content: center;
}

.artifact-preview-resize-line {
    width: 2px;
    height: 48px;
    border-radius: 1px;
    background: var(--td-component-border);
    opacity: 0.55;
    transition: opacity 0.15s ease, background 0.15s ease;
}

.artifact-preview-resize-handle:hover .artifact-preview-resize-line,
.artifact-preview-resize-handle--active .artifact-preview-resize-line {
    opacity: 1;
    background: var(--td-brand-color);
}
</style>

<style lang="less">
.chat-artifacts-drawer.t-drawer {
    .t-drawer__header {
        padding: 16px 20px;
        font-weight: normal;
    }

    .t-drawer__body {
        padding: 4px 20px 16px;
    }
}

.t-drawer.chat-artifacts-drawer--preview {
    .t-drawer__content-wrapper,
    .t-drawer__content {
        height: 100%;
    }

    .t-drawer__header {
        padding: 14px 18px;
        border-bottom: 1px solid var(--td-component-stroke);
        flex-shrink: 0;
    }

    .t-drawer__body {
        flex: 1;
        min-height: 0;
        padding: 12px 16px 16px;
        display: flex;
        flex-direction: column;
        overflow: hidden;
    }
}

.t-drawer.chat-artifacts-drawer--resizing .t-drawer__content {
    transition: none !important;
}

.t-drawer.chat-artifacts-drawer--resizing {
    .artifact-preview-body,
    .document-preview,
    iframe,
    .pdf-iframe,
    .html-iframe {
        pointer-events: none;
        user-select: none;
    }
}
</style>
