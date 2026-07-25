<template>
    <div class="main" :class="{ 'main--compact': uiStore.compactViewport }" ref="dropzone">
        <header v-if="uiStore.compactViewport" class="compact-topbar">
            <button type="button" class="compact-topbar__button" :aria-label="t('menu.expandSidebar')"
                @click="uiStore.toggleSidebar">
                <svg viewBox="0 0 24 24" width="22" height="22" fill="none" aria-hidden="true">
                    <path d="M4 7h16M4 12h16M4 17h16" stroke="currentColor" stroke-width="1.8"
                        stroke-linecap="round" />
                </svg>
            </button>
            <img class="compact-topbar__logo" src="@/assets/img/weknora.png" alt="WeKnora">
            <button type="button" class="compact-topbar__button" :aria-label="t('menu.search')"
                @click="commandPaletteStore.openPalette('')">
                <svg viewBox="0 0 24 24" width="21" height="21" fill="none" aria-hidden="true">
                    <circle cx="11" cy="11" r="6.5" stroke="currentColor" stroke-width="1.7" />
                    <path d="m16 16 4 4" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" />
                </svg>
            </button>
        </header>
        <Menu></Menu>
        <div v-if="isRouterAlive" class="platform-route-outlet">
            <RouterView />
        </div>
        <div class="upload-mask" v-show="ismask">
            <input type="file" style="display: none" ref="uploadInput" accept=".pdf,.docx,.doc,.pptx,.ppt,.epub,.mhtml,.txt,.md,.jpg,.jpeg,.png,.csv,.xls,.xlsx" />
            <UploadMask></UploadMask>
        </div>
        <!-- 全局设置模态框，供所有 platform 子路由使用 -->
        <Settings />
        <!-- 全局命令面板 (⌘K)，随 platform 路由存活 -->
        <GlobalCommandPalette />
        <!-- 全局右上角"待处理邀请"铃铛。固定定位，z-index 低于抽屉，业务页面
             右侧抽屉弹出时会自然覆盖；仅在有待处理邀请时渲染。 -->
        <GlobalInvitationBell />
        <!-- 带遮罩层的新手引导：首次进入自动开启，可从用户菜单顶部昵称旁帮助按钮重新打开 -->
        <NewUserGuide />
    </div>
</template>
<script setup lang="ts">
import Menu from '@/components/menu.vue'
import { ref, onMounted, onUnmounted, nextTick, provide, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router'
import useKnowledgeBase from '@/hooks/useKnowledgeBase'
import UploadMask from '@/components/upload-mask.vue'
import Settings from '@/views/settings/Settings.vue'
import GlobalCommandPalette from '@/components/GlobalCommandPalette.vue'
import GlobalInvitationBell from '@/components/GlobalInvitationBell.vue'
import NewUserGuide from '@/components/NewUserGuide.vue'
import { useCommandPaletteStore } from '@/stores/commandPalette'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useUIStore } from '@/stores/ui'
import { getKnowledgeBaseById } from '@/api/knowledge-base/index'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

let { requestMethod } = useKnowledgeBase()
const route = useRoute();
const router = useRouter();
const commandPaletteStore = useCommandPaletteStore();
const uiStore = useUIStore();
let ismask = ref(false)
let uploadInput = ref();
const { t } = useI18n();

const isRouterAlive = ref(true)
const reloadApp = () => {
    isRouterAlive.value = false
    nextTick(() => {
        isRouterAlive.value = true
    })
}
provide('app:reload', reloadApp)

// 仅在 Wails 桌面端运行时拦截 Cmd/Ctrl+R：
// 桌面端没有浏览器地址栏，整页重载会白屏，所以用前端软刷新替代。
// 浏览器（含 Web 版 / 非 Lite 部署）里不拦截，交给浏览器做真正的整页刷新，
// 否则会出现左侧菜单、全局设置、Pinia store 等不随"刷新"一起重置的问题。
// @ts-ignore
const isWailsDesktop = typeof window !== 'undefined' && !!(window as any).runtime?.EventsOn

const handleGlobalKeyDown = (e: KeyboardEvent) => {
    if (!isWailsDesktop) return
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'r') {
        e.preventDefault()
        reloadApp()
    }
}

// 用于跟踪拖拽进入/离开的计数器，解决子元素触发 dragleave 的问题
let dragCounter = 0;

// 获取当前知识库ID
const getCurrentKbId = (): string | null => {
    return (route.params as any)?.kbId as string || null
}

const CHAT_DROP_ROUTE_NAMES = new Set(['chat', 'globalCreatChat', 'kbCreatChat']);

const isChatDropRoute = () => {
    return CHAT_DROP_ROUTE_NAMES.has(String(route.name || ''));
}

const collectDroppedFiles = async (event: DragEvent): Promise<File[]> => {
    const dataTransferFiles = event.dataTransfer?.files ? Array.from(event.dataTransfer.files) : [];
    if (dataTransferFiles.length > 0) {
        return dataTransferFiles;
    }

    const dataTransferItems = event.dataTransfer?.items ? Array.from(event.dataTransfer.items) : [];
    if (dataTransferItems.length === 0) {
        return [];
    }

    const files = await Promise.all(dataTransferItems.map(item => new Promise<File | null>((resolve) => {
        const fileEntry = (item as any).webkitGetAsEntry?.();
        if (fileEntry?.isFile && typeof fileEntry.file === 'function') {
            fileEntry.file((file: File) => resolve(file), () => resolve(null));
            return;
        }
        resolve(null);
    })));

    return files.filter((file): file is File => file instanceof File);
}

// 检查知识库初始化状态
const checkKnowledgeBaseInitialization = async (): Promise<boolean> => {
    const currentKbId = getCurrentKbId();
    
    if (!currentKbId) {
        MessagePlugin.error(t('knowledgeBase.missingId'));
        return false;
    }
    
    try {
        const kbResponse = await getKnowledgeBaseById(currentKbId);
        const kb = kbResponse.data;
        
        if (!kb.summary_model_id) {
            MessagePlugin.warning(t('knowledgeBase.notInitialized'));
            return false;
        }
        const strategy = kb.indexing_strategy;
        const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled;
        if (needsEmbedding && !kb.embedding_model_id) {
            MessagePlugin.warning(t('knowledgeBase.notInitialized'));
            return false;
        }
        return true;
    } catch (error) {
        MessagePlugin.error(t('knowledgeBase.getInfoFailed'));
        return false;
    }
}


// isFileDrag distinguishes an OS file drag (the only thing the global upload
// drop zone cares about) from an in-app element drag such as the wiki
// folder/page drag-and-drop. Element drags carry only "text/*" types, never
// "Files", so we bail out and let the originating component handle the drop.
const isFileDrag = (event: DragEvent): boolean => {
    const types = event.dataTransfer?.types
    if (!types) return false
    return Array.from(types).includes('Files')
}

// 全局拖拽事件处理
const handleGlobalDragEnter = (event: DragEvent) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    dragCounter++;
    if (event.dataTransfer) {
        event.dataTransfer.effectAllowed = 'all';
    }
    ismask.value = true;
}

const handleGlobalDragOver = (event: DragEvent) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    if (event.dataTransfer) {
        event.dataTransfer.dropEffect = 'copy';
    }
}

const handleGlobalDragLeave = (event: DragEvent) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    dragCounter--;
    if (dragCounter === 0) {
        ismask.value = false;
    }
}

const handleGlobalDrop = async (event: DragEvent) => {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    dragCounter = 0;
    ismask.value = false;

    const droppedFiles = await collectDroppedFiles(event);
    if (droppedFiles.length === 0) {
        MessagePlugin.warning(t('knowledgeBase.dragFileNotText'));
        return;
    }

    if (isChatDropRoute()) {
        event.stopPropagation();
        window.dispatchEvent(new CustomEvent('weknora:chat-file-drop', {
            detail: { files: droppedFiles }
        }));
        return;
    }
    
    const isInitialized = await checkKnowledgeBaseInitialization();
    if (!isInitialized) {
        return;
    }

    droppedFiles.forEach(file => requestMethod(file, uploadInput));
}

const compactViewportQuery = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(max-width: 768px)')
    : null;

const syncCompactViewport = (query: MediaQueryList | MediaQueryListEvent | null) => {
    uiStore.setCompactViewport(Boolean(query?.matches));
};

// 组件挂载时添加全局事件监听器
onMounted(() => {
    document.addEventListener('dragenter', handleGlobalDragEnter, true);
    document.addEventListener('dragover', handleGlobalDragOver, true);
    document.addEventListener('dragleave', handleGlobalDragLeave, true);
    document.addEventListener('drop', handleGlobalDrop, true);
    syncCompactViewport(compactViewportQuery);
    compactViewportQuery?.addEventListener('change', syncCompactViewport);
    if (isWailsDesktop) {
        window.addEventListener('keydown', handleGlobalKeyDown);
        // @ts-ignore
        window.runtime.EventsOn('app:reload', () => {
            reloadApp()
        })
    }
    // 支持通过 URL 查询参数打开全局命令面板，例如旧路径
    // /platform/knowledge-search?q=foo 重定向后携带 ?cmdk=foo
    maybeOpenCmdkFromRoute()
    // 后台预取对话输入栏资源，进入 creatChat / chat 时复用缓存
    void useChatResourcesStore().prefetchChatInput()
});

// 监听路由变化，兼容 SPA 内部跳转时的 ?cmdk= 参数
watch(() => route.query.cmdk, () => {
    maybeOpenCmdkFromRoute()
})

watch(() => route.fullPath, () => {
    if (uiStore.compactViewport) {
        uiStore.closeMobileSidebar()
    }
})

function maybeOpenCmdkFromRoute() {
    if (!('cmdk' in route.query)) return
    const q = String(route.query.cmdk ?? '')
    commandPaletteStore.openPalette(q)
    // 清除 query，避免回退/刷新时反复触发
    const newQuery = { ...route.query }
    delete (newQuery as any).cmdk
    router.replace({ path: route.path, query: newQuery, hash: route.hash })
}

// 组件卸载时移除全局事件监听器
onUnmounted(() => {
    document.removeEventListener('dragenter', handleGlobalDragEnter, true);
    document.removeEventListener('dragover', handleGlobalDragOver, true);
    document.removeEventListener('dragleave', handleGlobalDragLeave, true);
    document.removeEventListener('drop', handleGlobalDrop, true);
    if (isWailsDesktop) {
        window.removeEventListener('keydown', handleGlobalKeyDown);
        // @ts-ignore
        if (window.runtime?.EventsOff) {
            // @ts-ignore
            window.runtime.EventsOff('app:reload')
        }
    }
    compactViewportQuery?.removeEventListener('change', syncCompactViewport);
    uiStore.setCompactViewport(false);
    dragCounter = 0;
});
</script>
<style lang="less">
.main {
    display: flex;
    align-items: stretch;
    width: 100%;
    height: 100%;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
    /* 统一整页背景，让左侧菜单与右侧内容区视觉连贯 */
    background: var(--td-bg-color-container);
}

/* 右侧路由区：占满剩余宽度与整列高度，并把 min-height:0 传给子页面以便内部 flex 滚动 */
.platform-route-outlet {
    flex: 1;
    min-width: 0;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.compact-topbar {
    display: none;
}

.upload-mask {
    background-color: rgba(255, 255, 255, 0.8);
    position: fixed;
    width: 100%;
    height: 100%;
    z-index: 999;
    display: flex;
    justify-content: center;
    align-items: center;
}

img {
    -webkit-user-drag: none;
    -khtml-user-drag: none;
    -moz-user-drag: none;
    -o-user-drag: none;
    user-drag: none;
}

@media (max-width: 768px) {
    .main--compact {
        position: relative;
        display: block;
    }

    .main--compact .platform-route-outlet {
        width: 100%;
        height: 100%;
        padding-top: var(--app-compact-header-height);
        box-sizing: border-box;
    }

    .compact-topbar {
        position: absolute;
        inset: 0 0 auto;
        z-index: 910;
        height: var(--app-compact-header-height);
        display: grid;
        grid-template-columns: 44px minmax(0, 1fr) 44px;
        align-items: center;
        padding: 0 8px;
        box-sizing: border-box;
        background: color-mix(in srgb, var(--td-bg-color-container) 94%, transparent);
        border-bottom: 1px solid var(--td-component-stroke);
        backdrop-filter: blur(12px);
    }

    .compact-topbar__button {
        width: 40px;
        height: 40px;
        padding: 0;
        border: 0;
        border-radius: 8px;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        color: var(--td-text-color-primary);
        background: transparent;
    }

    .compact-topbar__button:active {
        background: var(--td-bg-color-container-active);
    }

    .compact-topbar__logo {
        justify-self: center;
        width: min(132px, 42vw);
        height: auto;
        object-fit: contain;
    }

    html[theme-mode="dark"] .compact-topbar__logo {
        filter: invert(1) hue-rotate(180deg);
    }
}
</style>
