<template>
    <!--
        ChatArtifactsDrawer — right-side drawer that lists every skill-generated
        file attached to the surrounding assistant message.

        Usage (from botmsg.vue / AgentStreamDisplay.vue):
            <ChatArtifactsDrawer
                v-model:visible="showArtifacts"
                :session-id="sessionId"
                :message-id="messageId"
                :artifacts="artifacts"
            />

        The list is driven by whatever the parent has already resolved from
        the streamed message payload. When the parent has no local copy
        (e.g. after a page refresh) it can pass an empty array and the
        drawer will pull the metadata itself via /artifacts.
    -->
    <t-drawer
        v-model:visible="internalVisible"
        class="chat-artifacts-drawer"
        placement="right"
        size="440px"
        attach="body"
        :footer="false"
        :close-on-overlay-click="true"
        :destroy-on-close="false"
        @close="handleClose"
    >
        <template #header>
            <div class="artifact-drawer-header">
                <div class="artifact-drawer-header-icon">
                    <t-icon name="file" />
                </div>
                <div class="artifact-drawer-header-title">{{ $t('agent.artifactDrawer.title') }}</div>
            </div>
        </template>
        <div v-if="loading" class="artifact-drawer-empty">
            <t-loading size="small" />
            <span>{{ $t('common.loading') }}</span>
        </div>
        <div v-else-if="!items.length" class="artifact-drawer-empty">
            <t-icon name="folder-open" size="32px" />
            <span>{{ $t('agent.artifactDrawer.empty') }}</span>
        </div>
        <ul v-else class="artifact-list">
            <li v-for="item in items" :key="`${item.index}-${item.file_name}`" class="artifact-item">
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
 *   - `items` prefers the props-provided list (already in memory from the
 *     stream payload). When empty, we pull from /artifacts on open so a
 *     refreshed page still shows something without waiting for the parent
 *     to re-hydrate.
 *   - Errors during download are surfaced via MessagePlugin.error but do
 *     NOT close the drawer, matching spec §7: "抽屉保持打开以便重试其他文件".
 */
import { computed, ref, watch, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { downloadArtifact, listMessageArtifacts, type ArtifactMeta } from '@/api/chat'
import { getFileIcon } from '@/utils/files'

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

// Two-way binding shim so `v-model:visible` works while the parent still
// owns the source of truth.
const internalVisible = computed({
    get: () => props.visible,
    set: (v: boolean) => emit('update:visible', v),
})

const loading = ref(false)
const fetched = ref<ArtifactMeta[]>([])
const downloading = reactive<Record<number, boolean>>({})

const items = computed<ArtifactMeta[]>(() => {
    if (props.artifacts && props.artifacts.length) return props.artifacts
    return fetched.value
})

// Refresh /artifacts when the drawer opens without a caller-provided list.
// Cheap when the caller already passed artifacts; a real request only when
// the parent's copy is empty (e.g. right after page refresh, before message
// hydration finishes).
watch(
    () => props.visible,
    async (open) => {
        if (!open) return
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

function handleClose() {
    emit('update:visible', false)
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
        // Trigger browser save via an object URL so the file name comes
        // through even when the server's Content-Disposition is stripped
        // by an intermediate proxy.
        const url = URL.createObjectURL(blob)
        const a = document.createElement('a')
        a.href = url
        a.download = item.file_name || 'artifact'
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        // Give the browser a tick to start the download before revoking.
        setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (err) {
        console.error('[ChatArtifactsDrawer] download failed:', err)
        MessagePlugin.error(t('agent.artifactDrawer.downloadFailed'))
    } finally {
        downloading[item.index] = false
    }
}
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
    font-size: 15px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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

    &:last-child {
        border-bottom: none;
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

.artifact-download {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);

    :deep(.t-button__icon) {
        margin: 0;
    }
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
</style>
