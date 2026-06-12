<template>
    <div class="aside_box" :class="{ 'aside_box--collapsed': uiStore.sidebarCollapsed }">
        <!-- 展开时：Logo + 搜索/折叠按钮同行 -->
        <div class="logo_row" v-if="!uiStore.sidebarCollapsed">
            <div class="logo_box" @click="router.push('/platform/knowledge-bases')" style="cursor: pointer;">
                <img class="logo" src="@/assets/img/weknora.png" alt="">
                <sup v-if="isLiteEdition" class="lite-badge">Lite</sup>
            </div>
            <div class="logo_actions">
                <t-tooltip placement="bottom">
                    <template #content>
                        <span class="cmdk-tip">
                            <span class="cmdk-tip-label">{{ t('menu.search') }}</span>
                            <span class="cmdk-tip-keys">{{ cmdModKeyLabel }}K</span>
                        </span>
                    </template>
                    <div class="header-icon-btn" @click="commandPaletteStore.openPalette('')"
                        :aria-label="t('menu.search')">
                        <img class="header-icon-img" :src="getImgSrc('search.svg')" alt="">
                    </div>
                </t-tooltip>
                <div class="sidebar-toggle" @click="uiStore.toggleSidebar" :title="t('menu.collapseSidebar')">
                    <svg viewBox="0 0 20 20" width="18" height="18" fill="none" xmlns="http://www.w3.org/2000/svg">
                        <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor" stroke-width="1.2" />
                        <line x1="7.5" y1="1.5" x2="7.5" y2="18.5" stroke="currentColor" stroke-width="1.2" />
                        <line x1="4" y1="7.5" x2="4" y2="12.5" stroke="currentColor" stroke-width="1.2"
                            stroke-linecap="round" />
                    </svg>
                </div>
            </div>
        </div>
        <!-- 折叠时：展开按钮 -->
        <t-tooltip v-else :content="t('menu.expandSidebar')" placement="right">
            <div class="menu_item sidebar-toggle-item" @click="uiStore.toggleSidebar">
                <div class="menu_item-box">
                    <div class="menu_icon">
                        <svg class="icon" viewBox="0 0 20 20" width="20" height="20" fill="none"
                            xmlns="http://www.w3.org/2000/svg">
                            <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor"
                                stroke-width="1.2" />
                            <line x1="7.5" y1="1.5" x2="7.5" y2="18.5" stroke="currentColor" stroke-width="1.2" />
                            <line x1="5" y1="10" x2="3" y2="8" stroke="currentColor" stroke-width="1.2"
                                stroke-linecap="round" />
                            <line x1="5" y1="10" x2="3" y2="12" stroke="currentColor" stroke-width="1.2"
                                stroke-linecap="round" />
                        </svg>
                    </div>
                </div>
            </div>
        </t-tooltip>

        <!-- 租户选择器：仅在用户可切换租户时显示 -->
        <TenantSelector v-if="canAccessAllTenants && !uiStore.sidebarCollapsed" />

        <!-- 折叠时右侧拖拽展开手柄 -->
        <div v-if="uiStore.sidebarCollapsed" class="sidebar-drag-handle" @mousedown="onDragHandleMouseDown" />

        <!-- 上半部分：新对话吸顶 + 知识库/智能体/共享空间/历史会话随滚动一起滚走 -->
        <div class="menu_top" ref="scrollContainer" @scroll="handleScroll">
            <!-- 全局搜索入口：点击打开命令面板（⌘K）。展开态移至顶部 logo_row 的图标按钮；
                 折叠态在此处保留为图标项 + 深色 tooltip。 -->
            <div class="menu_box menu_box--cmdk" v-if="uiStore.sidebarCollapsed">
                <t-tooltip placement="right">
                    <template #content>
                        <span class="cmdk-tip">
                            <span class="cmdk-tip-label">{{ t('menu.search') }}</span>
                            <span class="cmdk-tip-keys">{{ cmdModKeyLabel }}K</span>
                        </span>
                    </template>
                    <div class="menu_item menu_item--cmdk" @click="commandPaletteStore.openPalette('')">
                        <div class="menu_item-box">
                            <div class="menu_icon">
                                <img class="icon" :src="getImgSrc('search.svg')" alt="">
                            </div>
                        </div>
                    </div>
                </t-tooltip>
            </div>
            <div class="menu_box" :class="{ 'menu_box--sticky': item.children && !uiStore.sidebarCollapsed }"
                v-for="(item, index) in topMenuItems" :key="index">
                <t-tooltip :content="item.title" placement="right" :disabled="!uiStore.sidebarCollapsed">
                    <div @click="handleMenuClick(item.path)" @mouseenter="mouseenteMenu(item.path)"
                        @mouseleave="mouseleaveMenu(item.path)" :data-guide="`nav-${item.path}`"
                        :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : isMenuItemActive(item.path) ? 'menu_item_active' : '']">
                        <div class="menu_item-box">
                            <div class="menu_icon">
                                <img class="icon"
                                    :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon : item.icon == 'agent' ? agentIcon : item.icon == 'organization' ? organizationIcon : item.icon == 'logout' ? logoutIcon : item.icon == 'setting' ? settingIcon : prefixIcon)"
                                    alt="">
                            </div>
                            <template v-if="!uiStore.sidebarCollapsed">
                                <span class="menu_title" :title="item.title">{{ item.title }}</span>
                                <span v-if="item.path === 'organizations' && orgStore.totalPendingJoinRequestCount > 0"
                                    class="menu-pending-badge"
                                    :title="t('organization.settings.pendingJoinRequestsBadge')">{{
                                        orgStore.totalPendingJoinRequestCount }}</span>
                            </template>
                        </div>
                    </div>
                </t-tooltip>
            </div>

            <!-- 历史会话：OpenAI 式「渠道文件夹 + 聊天列表」，各来源独立分页 -->
            <div class="submenu" v-if="!uiStore.sidebarCollapsed">
                <template v-if="sessionListBooting && !hasAnySession">
                    <div v-for="n in 4" :key="'skel-' + n" class="submenu_item_p session-chat-row">
                        <div class="session-list-row session-list-row--flat">
                            <t-skeleton animation="gradient" class="session-list-row__body"
                                :row-col="[{ width: '100%', height: '14px' }]" />
                        </div>
                    </div>
                </template>

                <!-- 其他来源（IM / 网页嵌入）：置顶一行入口，默认收起 -->
                <div v-if="visibleChannelBuckets.length" class="session-channels-section">
                    <div class="timeline_header timeline_header--collapsible session-list-row session-list-row--flat"
                        role="button" tabindex="0"
                        @click="toggleChannelsSection"
                        @keydown.enter.prevent="toggleChannelsSection"
                        @keydown.space.prevent="toggleChannelsSection">
                        <span class="session-list-row__body">
                            <span class="session-header-text">
                                <span class="timeline_header-label">{{ t('menu.otherSources') }}</span>
                                <t-icon class="timeline_header-chevron"
                                    :name="channelsSectionCollapsed ? 'chevron-right' : 'chevron-down'"
                                    size="10px" />
                            </span>
                        </span>
                    </div>
                    <template v-if="!channelsSectionCollapsed">
                        <template v-for="bucket in visibleChannelBuckets" :key="bucket.key">
                            <div class="session-folder-header session-list-row session-list-row--flat"
                                role="button" tabindex="0"
                                @click="toggleSessionGroup(bucket.key)"
                                @keydown.enter.prevent="toggleSessionGroup(bucket.key)"
                                @keydown.space.prevent="toggleSessionGroup(bucket.key)">
                                <span class="session-list-row__body">
                                    <span class="session-header-text">
                                        <span class="session-folder-entry">
                                            <img v-if="bucket.platform && platformLogo(bucket.platform)"
                                                :src="platformLogo(bucket.platform)" :alt="bucket.platform"
                                                class="session-channel-inline-icon" />
                                            <t-icon v-else name="internet" class="session-channel-inline-icon"
                                                size="12px" />
                                            <span class="session-folder-label">{{ bucket.label }}</span>
                                        </span>
                                        <t-icon class="session-group-chevron"
                                            :name="isSessionGroupCollapsed(bucket.key) ? 'chevron-right' : 'chevron-down'"
                                            size="12px" />
                                    </span>
                                </span>
                            </div>
                            <template v-if="!isSessionGroupCollapsed(bucket.key)">
                                <div v-if="bucket.loading && bucket.items.length === 0"
                                    class="session-folder-loading">
                                    <div class="session-list-row session-list-row--flat">
                                        <span class="session-list-row__body">
                                            <t-loading size="small" />
                                        </span>
                                    </div>
                                </div>
                                <div v-for="subitem in bucket.items" :key="subitem.id"
                                    class="submenu_item_p session-chat-row session-folder-item">
                                    <div class="session-list-row session-list-row--flat">
                                        <div class="session-list-row__body">
                                    <SessionSidebarRow :item="subitem" :batch-mode="batchMode"
                                        :active-path="currentSecondpath" :selected-ids="batchSelectedIds"
                                        :menu-options="buildSessionMenuOptions(subitem)"
                                        @navigate="gotopage(subitem.path)"
                                        @toggle-select="toggleBatchSelect(subitem.id)"
                                        @menu-click="handleSessionMenuClick($event, subitem)"
                                        @hover-in="mouseenteBotDownr(subitem.id)"
                                        @hover-out="mouseleaveBotDown" />
                                        </div>
                                    </div>
                                </div>
                            </template>
                        </template>
                    </template>
                </div>

                <!-- 网页对话：按日期分组，直接展示 -->
                <div class="session-web-list">
                <template v-if="webBucket?.loaded && chatGroupedSessions.length === 0">
                    <div class="submenu_empty">{{ t('menu.noSessions') }}</div>
                </template>
                <template v-else>
                    <template v-for="group in chatGroupedSessions" :key="group.key">
                        <div v-if="group.label" class="timeline_header session-list-row session-list-row--flat">
                            <span class="session-list-row__body">
                                <span class="timeline_header-label">{{ group.label }}</span>
                            </span>
                        </div>
                        <div v-for="subitem in group.items" :key="subitem.id" class="submenu_item_p session-chat-row">
                            <div class="session-list-row session-list-row--flat">
                                <div class="session-list-row__body">
                            <SessionSidebarRow :item="subitem" :batch-mode="batchMode"
                                :active-path="currentSecondpath" :selected-ids="batchSelectedIds"
                                :menu-options="buildSessionMenuOptions(subitem)"
                                @navigate="gotopage(subitem.path)" @toggle-select="toggleBatchSelect(subitem.id)"
                                @menu-click="handleSessionMenuClick($event, subitem)"
                                @hover-in="mouseenteBotDownr(subitem.id)" @hover-out="mouseleaveBotDown" />
                                </div>
                            </div>
                        </div>
                    </template>
                    <div v-if="webBucket?.loading && chatGroupedSessions.length > 0"
                        class="session-list-loading session-list-row session-list-row--flat">
                        <span class="session-list-row__body">
                            <t-loading size="small" />
                        </span>
                    </div>
                </template>
                </div>
            </div>

            <!-- 批量管理底部操作条 -->
            <div v-if="batchMode && !uiStore.sidebarCollapsed" class="batch-inline-footer">
                <div class="batch-footer-left">
                    <t-checkbox :checked="isAllBatchSelected" :indeterminate="isBatchIndeterminate"
                        @change="toggleBatchSelectAll">
                        {{ t('batchManage.selectAll') }}
                    </t-checkbox>
                </div>
                <div class="batch-footer-right">
                    <t-button size="small" variant="text" @click="exitBatchMode">
                        {{ t('batchManage.cancel') }}
                    </t-button>
                    <t-button size="small" theme="danger" variant="base" :disabled="batchSelectedIds.length === 0"
                        :loading="batchDeleting" @click="handleInlineBatchDelete">
                        {{ t('batchManage.delete') }}{{ batchSelectedIds.length > 0 ? `(${batchDisplayCount})` : '' }}
                    </t-button>
                </div>
            </div>
        </div>


        <!-- 下半部分：用户菜单 -->
        <div class="menu_bottom">
            <UserMenu />
        </div>

    </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia';
import { onMounted, onUnmounted, watch, computed, ref, h, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getSessionsList, delSession, batchDelSessions, deleteAllSessions, clearSessionMessages, pinSession, unpinSession } from "@/api/chat/index";
import { useChatResourcesStore } from '@/stores/chatResources';
import { listAllIMChannels } from '@/api/agent/index';
import SessionSidebarRow from './SessionSidebarRow.vue';
import {
    SIDEBAR_BUCKET_PAGE_SIZE,
    applyBucketCountProbe,
    buildBucketDefinitions,
    bucketHasMore,
    bucketVisible,
    createEmptyBucket,
    flattenBucketItems,
    isChannelBucket,
    isChannelBucketKey,
    mergeBucketPage,
    prependSessionToWebBucket,
    removeSessionFromBuckets,
    type SidebarSessionBucket,
} from './sessionSidebarBuckets';
import type { SessionForGrouping } from './sessionGrouping';
import { listAllEmbedChannels } from '@/api/embed/index';
import {
    classifyDateBucket,
    configuredPlatforms,
    groupSessionsByDate,
    type DateBucketKey,
} from './sessionGrouping';
import { logout as logoutApi } from '@/api/auth';
import { useMenuStore } from '@/stores/menu';
import { useAuthStore } from '@/stores/auth';
import { useOrganizationStore } from '@/stores/organization';
import { useUIStore } from '@/stores/ui';
import { useCommandPaletteStore } from '@/stores/commandPalette';
import { MessagePlugin, DialogPlugin, Icon as TIcon } from "tdesign-vue-next";
import UserMenu from '@/components/UserMenu.vue';
import TenantSelector from '@/components/TenantSelector.vue';
import { useI18n } from 'vue-i18n';
import { getSystemInfo } from '@/api/system';

const chatResources = useChatResourcesStore();
// Platform logos reused from IMChannelsOverviewPanel — keeps the session list
// visually consistent with the channels admin view.
import wecomLogo from '@/assets/img/im/wecom.svg';
import feishuLogo from '@/assets/img/im/feishu.svg';
import slackLogo from '@/assets/img/im/slack.svg';
import telegramLogo from '@/assets/img/im/telegram.svg';
import dingtalkLogo from '@/assets/img/im/dingtalk.svg';
import mattermostLogo from '@/assets/img/im/mattermost.svg';
import wechatLogo from '@/assets/img/im/wechat.svg';

const PLATFORM_LOGO: Record<string, string> = {
    wecom: wecomLogo,
    feishu: feishuLogo,
    slack: slackLogo,
    telegram: telegramLogo,
    dingtalk: dingtalkLogo,
    mattermost: mattermostLogo,
    wechat: wechatLogo,
};

const platformLogo = (p: string): string => (p ? PLATFORM_LOGO[p] || '' : '');

const { t } = useI18n();
const usemenuStore = useMenuStore();
const authStore = useAuthStore();
const orgStore = useOrganizationStore();
const uiStore = useUIStore();
const commandPaletteStore = useCommandPaletteStore();

// Platform-aware label for the ⌘K hint. navigator.platform is deprecated but
// the alternatives (userAgentData.platform) aren't universally available yet;
// this check is good enough for Mac vs. non-Mac.
const isMacLike = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform || '');
const cmdModKeyLabel = isMacLike ? '⌘' : 'Ctrl';
const route = useRoute();
const router = useRouter();
const currentpath = ref('');
const total = ref(0);
const sessionBuckets = ref<Record<string, SidebarSessionBucket>>({});
const bucketOrder = ref<string[]>([]);
let bucketRequestToken = 0;
const sessionListBooting = ref(false);
const currentSecondpath = ref('');
const scrollContainer = ref<HTMLElement | null>(null);
const imPlatforms = ref<string[]>([]);
const embedChannelNames = ref<Record<string, string>>({});
// 分组折叠：其他来源整块 + 各来源文件夹默认收起；日期标题仅作分隔，不折叠。
const channelsSectionCollapsed = ref(true);
const collapsedSessionGroups = ref<Set<string>>(new Set());
const channelBucketKeys = (keys: string[] = bucketOrder.value) =>
    keys.filter((key) => isChannelBucketKey(key));
const applyDefaultChannelCollapse = () => {
    collapsedSessionGroups.value = new Set(channelBucketKeys());
};
const isSessionGroupCollapsed = (key: string) => collapsedSessionGroups.value.has(key);
const toggleChannelsSection = async () => {
    channelsSectionCollapsed.value = !channelsSectionCollapsed.value;
    await refreshSessionListScrollability();
};
const ensureChannelBucketLoaded = async (key: string) => {
    if (!isChannelBucketKey(key)) return;
    const bucket = sessionBuckets.value[key];
    if (!bucket || bucket.loaded || bucket.loading) return;
    await loadBucketPage(key, 1);
};

const toggleSessionGroup = async (key: string) => {
    const wasCollapsed = collapsedSessionGroups.value.has(key);
    const next = new Set(collapsedSessionGroups.value);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    collapsedSessionGroups.value = next;
    const expanded = wasCollapsed && !next.has(key);
    if (expanded && isChannelBucketKey(key)) {
        await ensureChannelBucketLoaded(key);
        await ensureBucketFillsViewport(key);
    }
    await refreshSessionListScrollability();
};
const sessionListCanScroll = ref(false);
const webBucket = computed(() => sessionBuckets.value.web);
const visibleChannelBuckets = computed(() =>
    bucketOrder.value
        .map((key) => sessionBuckets.value[key])
        .filter((bucket): bucket is SidebarSessionBucket => !!bucket && isChannelBucket(bucket) && bucketVisible(bucket)),
);
const hasAnySession = computed(() =>
    Object.values(sessionBuckets.value).some((bucket) => bucket.items.length > 0),
);
type MenuItem = { title: string; icon: string; path: string; childrenPath?: string; children?: any[] };
const { menuArr, visibleMenuArr } = storeToRefs(usemenuStore);
let activeSubmenu = ref<string>('');
const isLiteEdition = ref(false);

// 批量管理状态
const batchMode = ref(false)
const batchSelectedIds = ref<string[]>([])
const batchDeleting = ref(false)

const allSessionIds = computed(() => {
    const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item: MenuItem) => item.path === 'creatChat');
    if (!chatMenu?.children) return [];
    return (chatMenu.children as any[]).map((s: any) => s.id);
})

const isAllBatchSelected = computed(() =>
    allSessionIds.value.length > 0 && batchSelectedIds.value.length === allSessionIds.value.length
)

const isBatchIndeterminate = computed(() =>
    batchSelectedIds.value.length > 0 && batchSelectedIds.value.length < allSessionIds.value.length
)

const batchDisplayCount = computed(() =>
    isAllBatchSelected.value ? total.value : batchSelectedIds.value.length
)

// 是否可以访问所有租户
const canAccessAllTenants = computed(() => authStore.canAccessAllTenants);

// 是否处于知识库详情页（不包括全局聊天）
const isInKnowledgeBase = computed<boolean>(() => {
    return route.name === 'knowledgeBaseDetail' ||
        route.name === 'kbCreatChat' ||
        route.name === 'knowledgeBaseSettings';
});

// 是否在知识库列表页面
const isInKnowledgeBaseList = computed<boolean>(() => {
    return route.name === 'knowledgeBaseList';
});

// 是否在创建聊天页面
const isInCreatChat = computed<boolean>(() => {
    return route.name === 'globalCreatChat' || route.name === 'kbCreatChat';
});

// 是否在对话详情页
const isInChatDetail = computed<boolean>(() => route.name === 'chat');

// 是否在智能体列表页面
const isInAgentList = computed<boolean>(() => route.name === 'agentList');

// 是否在组织列表页面
const isInOrganizationList = computed<boolean>(() => route.name === 'organizationList');

// 统一的菜单项激活状态判断
const isMenuItemActive = (itemPath: string): boolean => {
    const currentRoute = route.name;

    switch (itemPath) {
        case 'knowledge-bases':
            return currentRoute === 'knowledgeBaseList' ||
                currentRoute === 'knowledgeBaseDetail' ||
                currentRoute === 'knowledgeBaseSettings';
        case 'agents':
            return currentRoute === 'agentList';
        case 'organizations':
            return currentRoute === 'organizationList';
        case 'creatChat':
            return currentRoute === 'kbCreatChat' || currentRoute === 'globalCreatChat';
        case 'settings':
            return currentRoute === 'settings';
        default:
            return itemPath === currentpath.value;
    }
};

// 统一的图标激活状态判断
const getIconActiveState = (itemPath: string) => {
    const currentRoute = route.name;

    return {
        isKbActive: itemPath === 'knowledge-bases' && (
            currentRoute === 'knowledgeBaseList' ||
            currentRoute === 'knowledgeBaseDetail' ||
            currentRoute === 'knowledgeBaseSettings'
        ),
        isCreatChatActive: itemPath === 'creatChat' && (currentRoute === 'kbCreatChat' || currentRoute === 'globalCreatChat'),
        isSettingsActive: itemPath === 'settings' && currentRoute === 'settings',
        isChatActive: itemPath === 'chat' && currentRoute === 'chat'
    };
};

// 分离上下两部分菜单（使用 visibleMenuArr 以便 lite 模式过滤 logout）
const topMenuItems = computed<MenuItem[]>(() => {
    return (visibleMenuArr.value as unknown as MenuItem[]).filter((item: MenuItem) =>
        item.path === 'knowledge-bases' || item.path === 'agents' || item.path === 'organizations' || item.path === 'creatChat'
    );
});

const bottomMenuItems = computed<MenuItem[]>(() => {
    return (visibleMenuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => {
        if (item.path === 'knowledge-bases' || item.path === 'agents' || item.path === 'organizations' || item.path === 'creatChat') {
            return false;
        }
        return true;
    });
});

// 当前知识库信息
const currentKbName = ref<string>('')
const currentKbInfo = ref<any>(null)

// 进行中的置顶/取消置顶请求，避免重复点击
const pinningIds = ref<Set<string>>(new Set())

const dateBucketLabels = computed<Record<DateBucketKey, string>>(() => ({
    pinned: t('time.pinned'),
    today: t('time.today'),
    yesterday: t('time.yesterday'),
    last7Days: t('time.last7Days'),
    last30Days: t('time.last30Days'),
    lastYear: t('time.lastYear'),
    earlier: t('time.earlier'),
}));

// 「聊天」区内按日期分组（仅 web 会话）
const chatGroupedSessions = computed(() => {
    const web = sessionBuckets.value.web;
    if (!web?.items.length) return [];

    return groupSessionsByDate(
        web.items,
        dateBucketLabels.value,
        (session) => classifyDateBucket(session.updated_at || session.created_at),
    );
});

const refreshSessionListScrollability = async () => {
    await nextTick();
    const container = scrollContainer.value;
    sessionListCanScroll.value = !!container && container.scrollHeight > container.clientHeight + 1;
};

/** 列表未撑满滚动区时自动续页（按当前可见 DOM 测量，避免折叠导致误判） */
const ensureBucketFillsViewport = async (key: string) => {
    const MAX_ITERATIONS = 20;
    for (let i = 0; i < MAX_ITERATIONS; i++) {
        await nextTick();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        const container = scrollContainer.value;
        const bucket = sessionBuckets.value[key];
        if (!container || !bucket || !bucketHasMore(bucket) || bucket.loading) break;

        const hasOverflow = container.scrollHeight > container.clientHeight + 1;
        if (hasOverflow) break;

        const prevCount = bucket.items.length;
        await loadBucketPage(key);
        if ((sessionBuckets.value[key]?.items.length ?? 0) <= prevCount) break;
    }
};

const mouseenteBotDownr = (val: string) => {
    activeSubmenu.value = val;
}
const mouseleaveBotDown = () => {
    activeSubmenu.value = '';
}

const enterBatchMode = () => {
    batchMode.value = true
    batchSelectedIds.value = []
}

const exitBatchMode = () => {
    batchMode.value = false
    batchSelectedIds.value = []
}

const toggleBatchSelect = (id: string) => {
    const idx = batchSelectedIds.value.indexOf(id)
    if (idx > -1) {
        batchSelectedIds.value.splice(idx, 1)
    } else {
        batchSelectedIds.value.push(id)
    }
}

const toggleBatchSelectAll = (checked: boolean) => {
    batchSelectedIds.value = checked ? [...allSessionIds.value] : []
}

const handleInlineBatchDelete = () => {
    if (batchSelectedIds.value.length === 0) return
    const isDeleteAll = isAllBatchSelected.value
    const displayCount = batchDisplayCount.value
    const confirmDialog = DialogPlugin.confirm({
        header: t('batchManage.deleteConfirmTitle'),
        body: isDeleteAll
            ? t('batchManage.deleteAllConfirmBody') || t('batchManage.deleteConfirmBody', { count: displayCount })
            : t('batchManage.deleteConfirmBody', { count: displayCount }),
        confirmBtn: { content: t('batchManage.delete'), theme: 'danger' as const },
        cancelBtn: t('batchManage.cancel'),
        theme: 'warning',
        onConfirm: async () => {
            batchDeleting.value = true
            try {
                let res: any
                if (isDeleteAll) {
                    res = await deleteAllSessions()
                } else {
                    res = await batchDelSessions([...batchSelectedIds.value])
                }
                if (res && res.success === true) {
                    if (isDeleteAll) {
                        usemenuStore.clearMenuArr();
                        total.value = 0;
                        await getMessageList();
                    } else {
                        let next = sessionBuckets.value;
                        for (const id of batchSelectedIds.value) {
                            next = removeSessionFromBuckets(next, id);
                        }
                        sessionBuckets.value = next;
                        syncMenuStoreFromBuckets();
                    }
                    const currentChatId = route.params.chatid as string;
                    if (currentChatId && (isDeleteAll || batchSelectedIds.value.includes(currentChatId))) {
                        router.push('/platform/creatChat');
                    }
                    batchSelectedIds.value = []
                    MessagePlugin.success(t('batchManage.deleteSuccess'))
                    exitBatchMode()
                } else {
                    MessagePlugin.error(t('batchManage.deleteFailed'))
                }
            } catch {
                MessagePlugin.error(t('batchManage.deleteFailed'))
            }
            batchDeleting.value = false
            confirmDialog.destroy()
        },
    })
}

const handleSessionMenuClick = (data: { value: string }, item: any) => {
    if (data?.value === 'delete') {
        delCard(item);
    } else if (data?.value === 'clearMessages') {
        clearMessages(item);
    } else if (data?.value === 'batchManage') {
        enterBatchMode()
    } else if (data?.value === 'pin' || data?.value === 'unpin') {
        togglePin(item, data.value === 'pin');
    }
};

// 基于会话来源推导展示用的短标签已经被 platformLogo(<img>) 取代，Web 会话没有图标。

const buildSessionMenuOptions = (item: any) => {
    const options: any[] = [];
    if (item.is_pinned) {
        options.push({
            content: t('menu.unpin'),
            value: 'unpin',
            prefixIcon: () => h(TIcon, { name: 'pin', size: '16px' }),
        });
    } else {
        options.push({
            content: t('menu.pin'),
            value: 'pin',
            prefixIcon: () => h(TIcon, { name: 'pin', size: '16px' }),
        });
    }
    options.push(
        { content: t('menu.clearMessages'), value: 'clearMessages', prefixIcon: () => h(TIcon, { name: 'clear', size: '16px' }) },
        { content: t('menu.batchManage'), value: 'batchManage', prefixIcon: () => h(TIcon, { name: 'queue', size: '16px' }) },
        { content: t('upload.deleteRecord'), value: 'delete', theme: 'error', prefixIcon: () => h(TIcon, { name: 'delete', size: '16px' }) },
    );
    return options;
};

const updateSessionInBuckets = (
    sessionId: string,
    patch: Partial<{ is_pinned: boolean; pinned_at: string | null; title: string; isNoTitle?: boolean }>,
) => {
    const next: Record<string, SidebarSessionBucket> = {};
    for (const [key, bucket] of Object.entries(sessionBuckets.value)) {
        next[key] = {
            ...bucket,
            items: bucket.items.map((row) => (row.id === sessionId ? { ...row, ...patch } : row)),
        };
    }
    sessionBuckets.value = next;
    syncMenuStoreFromBuckets();
};

const togglePin = (item: any, pin: boolean) => {
    if (pinningIds.value.has(item.id)) return;
    pinningIds.value.add(item.id);

    const call = pin ? pinSession(item.id) : unpinSession(item.id);
    call.then((res: any) => {
        if (res && res.success) {
            updateSessionInBuckets(item.id, {
                is_pinned: pin,
                pinned_at: pin ? new Date().toISOString() : null,
            });
        } else {
            MessagePlugin.error(pin ? t('menu.pinFailed') : t('menu.unpinFailed'));
        }
    }).catch(() => {
        MessagePlugin.error(pin ? t('menu.pinFailed') : t('menu.unpinFailed'));
    }).finally(() => {
        pinningIds.value.delete(item.id);
    });
};

const clearMessages = (item: any) => {
    clearSessionMessages(item.id).then((res: any) => {
        if (res && res.success) {
            MessagePlugin.success(t('menu.clearMessagesSuccess'));
            if (item.id === route.params.chatid) {
                window.dispatchEvent(new CustomEvent('session-messages-cleared', { detail: { sessionId: item.id } }));
            }
        } else {
            MessagePlugin.error(t('menu.clearMessagesFailed'));
        }
    }).catch(() => {
        MessagePlugin.error(t('menu.clearMessagesFailed'));
    });
};

const delCard = (item: any) => {
    delSession(item.id).then((res: any) => {
        if (res && (res as any).success) {
            sessionBuckets.value = removeSessionFromBuckets(sessionBuckets.value, item.id);
            syncMenuStoreFromBuckets();

            if (item.id == route.params.chatid) {
                router.push('/platform/creatChat');
            }
        } else {
            MessagePlugin.error(t('chat.deleteSessionFailed'));
        }
    })
}


const debounce = (fn: (...args: any[]) => void, delay: number) => {
    let timer: ReturnType<typeof setTimeout>
    return (...args: any[]) => {
        clearTimeout(timer)
        timer = setTimeout(() => fn(...args), delay)
    }
}
const mapSessionRow = (item: any) => ({
    title: item.title ? item.title : t('menu.newSession'),
    path: `chat/${item.id}`,
    id: item.id,
    isMore: false,
    isNoTitle: item.title ? false : true,
    created_at: item.created_at,
    updated_at: item.updated_at,
    is_pinned: !!item.is_pinned,
    pinned_at: item.pinned_at || null,
    im_platform: item.im_platform || '',
    description: item.description || '',
});

const syncMenuStoreFromBuckets = () => {
    usemenuStore.clearMenuArr();
    const flat = flattenBucketItems(sessionBuckets.value, bucketOrder.value);
    flat.forEach((item) => usemenuStore.updatemenuArr(item));
    total.value = flat.length;
};

const menuChildToSessionRow = (item: Record<string, unknown>): SessionForGrouping & { path: string } => {
    const id = String(item.id);
    return {
        id,
        path: typeof item.path === 'string' ? item.path : `chat/${id}`,
        title: typeof item.title === 'string' ? item.title : undefined,
        is_pinned: !!item.is_pinned,
        created_at: typeof item.created_at === 'string' ? item.created_at : undefined,
        updated_at: typeof item.updated_at === 'string' ? item.updated_at : undefined,
        im_platform: typeof item.im_platform === 'string' ? item.im_platform : '',
        description: typeof item.description === 'string' ? item.description : '',
    };
};

const sessionExistsInBuckets = (sessionId: string) =>
    Object.values(sessionBuckets.value).some((bucket) => bucket.items.some((row) => row.id === sessionId));

/** 创建会话后 menuStore 已乐观写入，但列表实际渲染自 sessionBuckets，需补齐。 */
const ensureSessionInSidebar = (sessionId: string) => {
    if (!sessionId || sessionExistsInBuckets(sessionId)) return;

    const web = sessionBuckets.value.web;
    if (!web) return;

    const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item) => item.path === 'creatChat');
    const fromStore = (chatMenu?.children as Record<string, unknown>[] | undefined)
        ?.find((item) => item.id === sessionId);
    if (!fromStore) return;

    sessionBuckets.value = {
        ...sessionBuckets.value,
        web: prependSessionToWebBucket(web, menuChildToSessionRow(fromStore)),
    };
    total.value = flattenBucketItems(sessionBuckets.value, bucketOrder.value).length;
};

const rebuildBucketDefinitions = () => buildBucketDefinitions(
    imPlatforms.value,
    embedChannelNames.value,
    {
        web: t('menu.myChats'),
        imPlatform: (platform) => t(`agentEditor.im.${platform}`),
        embedChannel: (name) => name,
    },
);

/** 首屏轻量探测各渠道是否有会话（page_size=1 只取 total），避免展示空文件夹 */
const probeChannelBucketCounts = async (keys: string[], token: number) => {
    const targets = keys.filter((key) => isChannelBucketKey(key));
    await Promise.all(
        targets.map(async (key) => {
            const bucket = sessionBuckets.value[key];
            if (!bucket) return;
            try {
                const res: any = await getSessionsList(1, 1, bucket.apiSource);
                if (token !== bucketRequestToken) return;
                sessionBuckets.value = {
                    ...sessionBuckets.value,
                    [key]: applyBucketCountProbe(bucket, res?.total ?? 0),
                };
            } catch {
                if (token !== bucketRequestToken) return;
                sessionBuckets.value = {
                    ...sessionBuckets.value,
                    [key]: applyBucketCountProbe(bucket, 0),
                };
            }
        }),
    );
};

const loadBucketPage = async (key: string, page?: number, token?: number) => {
    const activeToken = token ?? bucketRequestToken;
    const bucket = sessionBuckets.value[key];
    if (!bucket || bucket.loading) return;

    const nextPage = page ?? bucket.page + 1;
    sessionBuckets.value = {
        ...sessionBuckets.value,
        [key]: { ...bucket, loading: true },
    };

    try {
        const res: any = await getSessionsList(nextPage, SIDEBAR_BUCKET_PAGE_SIZE, bucket.apiSource);
        if (activeToken !== bucketRequestToken) return;
        const rows = (res?.data || []).map((item: any) => mapSessionRow(item));
        const current = sessionBuckets.value[key];
        sessionBuckets.value = {
            ...sessionBuckets.value,
            [key]: mergeBucketPage(current, rows, res?.total ?? rows.length, nextPage),
        };
        syncMenuStoreFromBuckets();
        await refreshSessionListScrollability();
    } catch {
        if (activeToken !== bucketRequestToken) return;
        const current = sessionBuckets.value[key];
        sessionBuckets.value = {
            ...sessionBuckets.value,
            [key]: { ...current, loading: false, loaded: true },
        };
    }
};

const initSessionBuckets = async () => {
    const token = ++bucketRequestToken;
    sessionListBooting.value = true;

    const defs = rebuildBucketDefinitions();
    bucketOrder.value = defs.map((def) => def.key);
    const buckets: Record<string, SidebarSessionBucket> = {};
    for (const def of defs) {
        buckets[def.key] = createEmptyBucket(def);
    }
    sessionBuckets.value = buckets;

    // 首屏：拉 web 会话 + 轻量探测各渠道 count（不拉完整列表）；有会话的渠道才展示文件夹
    const channelKeys = defs.map((def) => def.key).filter((key) => isChannelBucketKey(key));
    await Promise.all([
        loadBucketPage('web', 1, token),
        probeChannelBucketCounts(channelKeys, token),
    ]);

    if (token === bucketRequestToken) {
        sessionListBooting.value = false;
        applyDefaultChannelCollapse();
        syncMenuStoreFromBuckets();
        await ensureBucketFillsViewport('web');
        await refreshSessionListScrollability();
    }
};

const getMessageList = async () => {
    await initSessionBuckets();
};

// 滚动到底时仅为「聊天」区加载下一页 web 会话
const checkScrollBottom = async () => {
    const container = scrollContainer.value;
    const web = sessionBuckets.value.web;
    if (!container || !web || !bucketHasMore(web) || web.loading) return;

    const { scrollTop, scrollHeight, clientHeight } = container;
    const hasOverflow = scrollHeight > clientHeight + 1;
    if (!hasOverflow) {
        await ensureBucketFillsViewport('web');
        return;
    }

    const isNearBottom = scrollHeight - (scrollTop + clientHeight) < 100;
    if (!isNearBottom) return;

    await loadBucketPage('web');
};

const handleScroll = debounce(checkScrollBottom, 200);

async function loadCurrentKbInfo(kbId: string) {
    if (!kbId || !isInKnowledgeBase.value) {
        currentKbName.value = ''
        currentKbInfo.value = null
        return
    }
    const data = await chatResources.fetchKnowledgeBaseById(kbId)
    if (data) {
        currentKbName.value = data.name || ''
        currentKbInfo.value = data
    } else {
        currentKbInfo.value = null
    }
}

const loadSessionOriginMeta = async () => {
    try {
        const res: any = await listAllIMChannels();
        imPlatforms.value = configuredPlatforms(res?.data || []);
    } catch {
        imPlatforms.value = [];
    }
    try {
        const res: any = await listAllEmbedChannels();
        const names: Record<string, string> = {};
        for (const ch of res?.data || []) {
            if (ch?.id && ch?.name) names[ch.id] = ch.name;
        }
        embedChannelNames.value = names;
    } catch {
        embedChannelNames.value = {};
    }
};

const handleSessionTitleUpdated = (event: Event) => {
    const detail = (event as CustomEvent<{ sessionId?: string; title?: string }>).detail;
    if (!detail?.sessionId || !detail.title) return;
    updateSessionInBuckets(detail.sessionId, { title: detail.title, isNoTitle: false });
};

onMounted(async () => {
    const routeName = typeof route.name === 'string' ? route.name : (route.name ? String(route.name) : '')
    currentpath.value = routeName;
    if (route.params.chatid) {
        currentSecondpath.value = `chat/${route.params.chatid}`;
    }

    window.addEventListener('session-title-updated', handleSessionTitleUpdated);

    isLiteEdition.value = authStore.isLiteMode
    getSystemInfo().then(res => {
        if (res.data?.edition === 'lite') {
            isLiteEdition.value = true
            authStore.setLiteMode(true)
        }
    }).catch(() => { })

    await loadCurrentKbInfo((route.params as any)?.kbId as string)

    await loadSessionOriginMeta();
    await getMessageList();
    const initialChatId = route.params.chatid as string | undefined;
    if (initialChatId) {
        ensureSessionInSidebar(initialChatId);
    }
    // 若组织列表未加载则拉取一次，用于侧栏「待审批」角标
    if (orgStore.organizations.length === 0) {
        orgStore.fetchOrganizations();
    }
});

onUnmounted(() => {
    window.removeEventListener('session-title-updated', handleSessionTitleUpdated);
});

watch([() => route.name, () => route.params], (newvalue, oldvalue) => {
    const nameStr = typeof newvalue[0] === 'string' ? (newvalue[0] as string) : (newvalue[0] ? String(newvalue[0]) : '')
    currentpath.value = nameStr;
    if (newvalue[1].chatid) {
        currentSecondpath.value = `chat/${newvalue[1].chatid}`;
    } else {
        currentSecondpath.value = "";
    }

    // 创建新会话时 creatChat 会先 updataMenuChildren，再跳转 chat/:id。
    // 侧栏实际渲染 sessionBuckets，需按 buckets 判断是否缺失，不能把 menuStore 当真相来源。
    const newChatId = (newvalue[1] as any)?.chatid as string | undefined;
    if (nameStr === 'chat' && newChatId) {
        ensureSessionInSidebar(newChatId);
    }

    // 路由变化时更新图标状态和知识库信息（不涉及对话列表）
    getIcon(nameStr);

    // 如果切换了知识库，更新知识库名称但不重新加载对话列表
    if (newvalue[1].kbId !== oldvalue?.[1]?.kbId) {
        loadCurrentKbInfo((newvalue[1] as any)?.kbId as string);
    }
});
let knowledgeIcon = ref('zhishiku-green.svg');
let prefixIcon = ref('prefixIcon.svg');
let logoutIcon = ref('logout.svg');
let settingIcon = ref('setting.svg');
let agentIcon = ref('agent.svg');
let organizationIcon = ref('organization.svg');
let pathPrefix = ref(route.name)
const getIcon = (path: string) => {
    // 根据当前路由状态更新所有图标
    const kbActiveState = getIconActiveState('knowledge-bases');
    const creatChatActiveState = getIconActiveState('creatChat');
    const settingsActiveState = getIconActiveState('settings');
    const agentsActiveState = route.name === 'agentList';
    const organizationsActiveState = route.name === 'organizationList';

    // 知识库图标：只在知识库页面显示绿色
    knowledgeIcon.value = kbActiveState.isKbActive ? 'zhishiku-green.svg' : 'zhishiku.svg';

    // 智能体图标：只在智能体页面显示绿色
    agentIcon.value = agentsActiveState ? 'agent-green.svg' : 'agent.svg';

    // 组织图标：只在组织页面显示绿色
    organizationIcon.value = organizationsActiveState ? 'organization-green.svg' : 'organization.svg';

    // 对话图标：只在对话创建页面显示绿色，其他情况显示默认
    prefixIcon.value = creatChatActiveState.isCreatChatActive ? 'prefixIcon-green.svg' : 'prefixIcon.svg';

    // 设置图标：只在设置页面显示绿色
    settingIcon.value = settingsActiveState.isSettingsActive ? 'setting-green.svg' : 'setting.svg';

    // 退出图标：始终显示默认
    logoutIcon.value = 'logout.svg';
}
getIcon(typeof route.name === 'string' ? route.name as string : (route.name ? String(route.name) : ''))
const handleMenuClick = async (path: string) => {
    if (path === 'knowledge-bases') {
        // 知识库菜单项：如果在知识库内部，跳转到当前知识库文件页；否则跳转到知识库列表
        const kbId = await getCurrentKbId()
        if (kbId) {
            router.push(`/platform/knowledge-bases/${kbId}`)
        } else {
            router.push('/platform/knowledge-bases')
        }
    } else if (path === 'agents') {
        router.push('/platform/agents')
    } else if (path === 'organizations') {
        // 组织菜单项：跳转到组织列表
        router.push('/platform/organizations')
    } else if (path === 'settings') {
        // 设置菜单项：打开设置弹窗并跳转路由
        uiStore.openSettings()
        router.push('/platform/settings')
    } else {
        gotopage(path)
    }
}

// 处理退出登录确认
const handleLogout = () => {
    gotopage('logout')
}

const getCurrentKbId = async (): Promise<string | null> => {
    const kbId = (route.params as any)?.kbId as string
    if (isInKnowledgeBase.value && kbId) {
        return kbId
    }
    return null
}

const gotopage = async (path: string) => {
    pathPrefix.value = path;
    // 处理退出登录
    if (path === 'logout') {
        try {
            // 调用后端API注销
            await logoutApi();
        } catch (error) {
            // 即使API调用失败，也继续执行本地清理
            console.error('注销API调用失败:', error);
        }
        // 清理所有状态和本地存储
        authStore.logout();
        MessagePlugin.success(t('menu.logoutSuccess'));
        router.push('/login');
        return;
    } else {
        if (path === 'creatChat') {
            // 如果在知识库详情页，跳转到全局对话创建页
            if (isInKnowledgeBase.value) {
                router.push('/platform/creatChat')
            } else {
                // 如果不在知识库内，进入对话创建页
                router.push(`/platform/creatChat`)
            }
        } else {
            router.push(`/platform/${path}`);
        }
    }
    getIcon(path)
}

const getImgSrc = (url: string) => {
    return new URL(`/src/assets/img/${url}`, import.meta.url).href;
}

const mouseenteMenu = (path: string) => {
}
const mouseleaveMenu = (path: string) => {
}

const onDragHandleMouseDown = (e: MouseEvent) => {
    e.preventDefault()
    const startX = e.clientX
    const expandThreshold = 40

    const onMouseMove = (ev: MouseEvent) => {
        if (ev.clientX - startX > expandThreshold) {
            uiStore.expandSidebar()
            cleanup()
        }
    }
    const onMouseUp = () => cleanup()
    const cleanup = () => {
        document.removeEventListener('mousemove', onMouseMove)
        document.removeEventListener('mouseup', onMouseUp)
    }
    document.addEventListener('mousemove', onMouseMove)
    document.addEventListener('mouseup', onMouseUp)
}


</script>
<style lang="less" scoped>
.aside_box {
    // 侧栏水平栅格：图标列与文案列统一对齐（Logo / 菜单 / 会话分组 / 会话行）
    --sidebar-inset-x: 14px;
    --sidebar-icon-size: 18px;
    --sidebar-channel-icon: 14px;
    --sidebar-icon-gap: 8px;
    --sidebar-text-inset: calc(var(--sidebar-inset-x) + var(--sidebar-icon-size) + var(--sidebar-icon-gap)); // 40px

    min-width: 260px;
    width: 260px;
    padding: 8px 6px 6px;
    background: var(--td-bg-color-sidebar);
    box-sizing: border-box;
    /* Avoid 100vh because <html> carries a `zoom` multiplier for font-size
       control; 100vh is evaluated against the unscaled viewport and then
       scaled, so at "large" the sidebar would extend past the window. The
       ancestor chain (html/body/#app/.main) is already height: 100%. */
    height: 100%;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    border-right: 1px solid var(--td-component-stroke);
    box-shadow: 1px 0 0 rgba(0, 0, 0, 0.02);
    transition: width 0.25s ease, min-width 0.25s ease;
    position: relative;

    // macOS Wails 桌面：红绿灯位于 HiddenInset 标题栏区域，需让出顶部空间
    html.wails-desktop & {
        padding-top: 30px;
    }

    &--collapsed {
        min-width: 60px;
        width: 60px;
        padding: 8px 3px 6px;
        overflow: visible;

        .menu_item {
            justify-content: center;
            padding: 9px 0;

            .menu_item-box {
                justify-content: center;
                width: auto;
            }

            .menu_icon {
                margin-right: 0;
            }
        }

        .menu_bottom {
            align-items: center;
        }

        .menu_top {
            margin-right: 0;
            padding-right: 0;
        }
    }

    .logo_row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        height: 50px;
        flex-shrink: 0;
        padding: 0 10px 0 var(--sidebar-inset-x);
    }

    .sidebar-toggle {
        display: flex;
        align-items: center;
        justify-content: center;
        width: 18px;
        height: 18px;
        flex-shrink: 0;
        cursor: pointer;
        color: var(--td-text-color-secondary);
        border-radius: 4px;
        transition: background-color 0.2s ease;
        box-sizing: border-box;

        &:hover {
            background: var(--td-bg-color-container-hover);
            color: var(--td-text-color-primary);
        }
    }

    .sidebar-drag-handle {
        position: absolute;
        top: 0;
        right: -3px;
        width: 6px;
        height: 100%;
        cursor: ew-resize;
        z-index: 10;

        &:hover {
            background: var(--td-brand-color-light);
        }
    }

    .logo_box {
        display: flex;
        align-items: center;
        flex: 1;
        min-width: 0;
        overflow: hidden;

        .logo {
            width: 128px;
            height: auto;
        }

        .lite-badge {
            margin-left: 2px;
            align-self: flex-start;
            margin-top: 2px;
            font-size: 9px;
            font-weight: 600;
            color: var(--td-text-color-placeholder);
            user-select: none;
            white-space: nowrap;
        }
    }

    .logo_img {
        margin-left: 24px;
        width: 30px;
        height: 30px;
        margin-right: 7.25px;
    }

    .logo_txt {
        transform: rotate(0.049deg);
        color: var(--td-text-color-primary);
        font-family: "TencentSans";
        font-size: 24.12px;
        font-style: normal;
        font-weight: W7;
        line-height: 21.7px;
    }

    .menu_top {
        flex: 1;
        display: flex;
        flex-direction: column;
        overflow-y: auto;
        overflow-x: hidden;
        min-height: 0;
        // 抵消 .aside_box 的右内边距，让滚动条贴近面板右缘；
        // 等量 padding 补回，保证列表文字位置不变。
        margin-right: -4px;
        padding-right: 4px;

        // Claude 风格细滚动条：默认透明，悬浮时显示一条圆角细灰条
        scrollbar-width: thin;
        scrollbar-color: transparent transparent;
        transition: scrollbar-color 0.2s ease;

        &::-webkit-scrollbar {
            width: 6px;
        }

        &::-webkit-scrollbar-track {
            background: transparent;
        }

        &::-webkit-scrollbar-thumb {
            background-color: transparent;
            border-radius: 6px;
            transition: background-color 0.2s ease;
        }

        &:hover {
            scrollbar-color: var(--td-scrollbar-color, rgba(0, 0, 0, 0.18)) transparent;

            &::-webkit-scrollbar-thumb {
                background-color: var(--td-scrollbar-color, rgba(0, 0, 0, 0.18));
            }
        }

        &::-webkit-scrollbar-thumb:hover {
            background-color: var(--td-scrollbar-hover-color, rgba(0, 0, 0, 0.32));
        }
    }

    .menu_bottom {
        flex-shrink: 0;
        display: flex;
        flex-direction: column;
    }

    .menu_box {
        display: flex;
        flex-direction: column;

        // 「新对话」吸顶：作为滚动容器(.menu_top)的直接子级，滚动时钉在顶部，
        // 知识库/智能体/共享空间及历史列表一起从其下方滚走。背景遮挡滚动内容。
        &--sticky {
            position: sticky;
            top: 0;
            z-index: 2;
            background: var(--td-bg-color-sidebar);
        }
    }


    .upload-file-wrap {
        padding: 6px;
        border-radius: 3px;
        height: 32px;
        width: 32px;
        box-sizing: border-box;
    }

    .upload-file-wrap:hover {
        background-color: var(--td-brand-color-light);
        color: var(--td-brand-color);

    }

    .upload-file-icon {
        width: 20px;
        height: 20px;
        color: var(--td-text-color-secondary);
    }

    .active-upload {
        color: var(--td-brand-color);
    }

    .menu_item_active {
        border-radius: 4px;
        background: var(--td-brand-color-light) !important;

        .menu_icon,
        .menu_title {
            color: var(--td-brand-color) !important;
        }
    }

    .menu_item_c_active {

        .menu_icon,
        .menu_title {
            color: var(--td-text-color-primary);
        }
    }

    .menu_p {
        height: 46px;
        padding: 3px 0;
        box-sizing: border-box;
    }

    .menu_item {
        cursor: pointer;
        display: flex;
        align-items: center;
        justify-content: space-between;
        height: 38px;
        padding: 8px 10px 8px var(--sidebar-inset-x);
        box-sizing: border-box;
        margin-bottom: 2px;
        border-radius: 4px;
        transition: background-color 0.2s ease;

        .menu_item-box {
            display: flex;
            align-items: center;
        }

        &:hover {
            border-radius: 4px;
            background: var(--td-bg-color-container-hover);

            .menu_icon,
            .menu_title {
                color: var(--td-text-color-primary);
            }
        }
    }

    .menu_icon {
        display: flex;
        flex: 0 0 var(--sidebar-icon-size);
        width: var(--sidebar-icon-size);
        margin-right: var(--sidebar-icon-gap);
        color: var(--td-text-color-secondary);

        .icon {
            width: 18px;
            height: 18px;
            overflow: hidden;
        }
    }

    .menu_title {
        color: var(--td-text-color-primary);
        text-overflow: ellipsis;
        font-family: var(--app-font-family);
        font-size: 14px;
        font-style: normal;
        font-weight: 600;
        line-height: 20px;
        overflow: hidden;
        white-space: nowrap;
        max-width: 120px;
        flex: 1;
    }

    .submenu {
        font-family: var(--app-font-family);
        font-size: 14px;
        font-style: normal;
        min-width: 0;
        padding-top: 3px;
    }

    :deep(.submenu_pin_icon) {
        color: inherit;
        font-size: 12px;
        margin-right: 4px;
        vertical-align: middle;
        flex-shrink: 0;
    }

    .submenu_source_icon {
        width: 14px;
        height: 14px;
        margin-right: 0px;
        vertical-align: middle;
        object-fit: contain;
        flex-shrink: 0;
        // 默认淡化处理，避免未选中状态下彩色图标与灰色标题不协调；
        // 悬浮或选中时恢复彩色，交互时才引人注意。
        filter: grayscale(1);
        opacity: 0.55;
        transition: filter 0.15s ease, opacity 0.15s ease;
    }

    :deep(.submenu_item:hover .submenu_source_icon),
    :deep(.submenu_item_active .submenu_source_icon) {
        filter: none;
        opacity: 1;
    }

    .session-channels-section {
        margin-bottom: 2px;

        .session-folder-header {
            font-size: 12px;
            line-height: 20px;
        }

        // 与置顶会话一致：图标内联在标题前，间距 4px
        .session-folder-entry {
            display: inline-flex;
            align-items: center;
            min-width: 0;
            flex: 1 1 auto;
            overflow: hidden;
        }

        .session-channel-inline-icon {
            flex-shrink: 0;
            width: 12px;
            height: 12px;
            margin-right: 4px;
            object-fit: contain;
            filter: grayscale(1);
            opacity: 0.55;
        }

        :deep(.session-channel-inline-icon.t-icon) {
            color: var(--td-text-color-secondary);
            opacity: 0.75;
        }
    }

    // 标题 + 折叠箭头紧挨排列，箭头 hover 时才出现
    // 列表行统一栅格：左缘 inset-x + 图标槽 18px + 间距 8px → 文案列与主菜单文字对齐
    .session-list-row {
        display: flex;
        align-items: center;
        gap: var(--sidebar-icon-gap);
        padding: 0 10px 0 var(--sidebar-inset-x);
        min-width: 0;
        box-sizing: border-box;
    }

    .session-list-row__icon {
        flex: 0 0 var(--sidebar-icon-size);
        width: var(--sidebar-icon-size);
        height: var(--sidebar-icon-size);
        display: inline-flex;
        align-items: center;
        justify-content: center;
        flex-shrink: 0;
    }

    .session-list-row__body {
        flex: 1 1 auto;
        min-width: 0;
        overflow: hidden;
    }

    // 聊天区分组标题 / 会话行：与「聊天」节标题同列左对齐，不再预留图标槽
    .session-list-row--flat {
        padding-left: var(--sidebar-inset-x);
        gap: 0;
    }

    // 渠道文件夹内会话：与文件夹标题文字列对齐
    .session-list-row--nested {
        padding-left: var(--sidebar-text-inset);
        gap: 0;
    }

    .session-header-text {
        display: inline-flex;
        align-items: center;
        gap: 3px;
        min-width: 0;
    }

    .session-group-chevron {
        flex: 0 0 auto;
        color: var(--td-text-color-placeholder);
        opacity: 0;
        transition: opacity 0.15s ease;
    }

    .session-folder-header {
        padding-top: 2px;
        padding-bottom: 2px;
        border-radius: 6px;
        cursor: pointer;
        color: var(--td-text-color-primary);
        font-size: 13px;
        line-height: 20px;
        user-select: none;
        outline: none;

        &:hover,
        &:focus-visible {
            background: var(--td-bg-color-container-hover);

            .session-group-chevron {
                opacity: 1;
            }
        }
    }

    .session-folder-icon {
        width: var(--sidebar-channel-icon);
        height: var(--sidebar-channel-icon);
        object-fit: contain;
        flex-shrink: 0;
    }

    .session-folder-label {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .session-folder-loading {
        display: flex;
        align-items: center;
        padding: 4px 0;
        color: var(--td-text-color-placeholder);

        .session-list-row {
            width: 100%;
        }
    }

    .session-list-loading {
        display: flex;
        align-items: center;
        min-height: 26px;
        color: var(--td-text-color-placeholder);
    }

    .timeline_header {
        font-family: var(--app-font-family);
        font-size: 11px;
        font-weight: 600;
        color: var(--td-text-color-disabled);
        padding-top: 4px;
        padding-bottom: 1px;
        margin-top: 0;
        line-height: 16px;
        user-select: none;
    }

    .timeline_header-label {
        white-space: nowrap;
    }

    .timeline_header--collapsible {
        cursor: pointer;
        outline: none;

        &:hover,
        &:focus-visible {
            color: var(--td-text-color-secondary);
        }
    }

    .timeline_header-chevron {
        flex: 0 0 auto;
        color: inherit;
        opacity: 0.55;
        transition: opacity 0.15s ease;
    }

    .timeline_header--collapsible:hover .timeline_header-chevron,
    .timeline_header--collapsible:focus-visible .timeline_header-chevron {
        opacity: 1;
    }

    .submenu_item_p {
        padding: 0;
        box-sizing: border-box;
        min-width: 0;
        overflow: hidden;

        &.session-chat-row .session-list-row {
            min-height: 30px;
            border-radius: 6px;
            transition: background 0.15s ease, color 0.15s ease;
        }

        &.session-chat-row:hover .session-list-row {
            background: var(--td-bg-color-container-hover);

            :deep(.menu-more) {
                color: var(--td-text-color-primary);
            }

            :deep(.menu-more-wrap) {
                opacity: 1;
            }
        }

        &.session-chat-row:has(:deep(.submenu_item_active)) .session-list-row {
            background: var(--td-brand-color-light);

            :deep(.submenu_item) {
                color: var(--td-brand-color);
            }

            :deep(.menu-more) {
                color: var(--td-brand-color);
            }

            :deep(.menu-more-wrap) {
                opacity: 1;
            }
        }

        &.session-chat-row:has(:deep(.submenu_item_selected)) .session-list-row {
            background: rgba(7, 192, 95, 0.05);
        }
    }

    // SessionSidebarRow 为子组件，需 :deep 才能让标题省略号生效
    :deep(.submenu_item) {
        cursor: pointer;
        display: flex;
        align-items: center;
        color: var(--td-text-color-primary);
        font-weight: 400;
        font-size: 14px;
        line-height: 20px;
        height: 100%;
        width: 100%;
        padding: 4px 0;
        position: relative;
        min-width: 0;
        background: transparent;

        .submenu_title {
            display: flex;
            align-items: center;
            flex: 1 1 auto;
            min-width: 0;
            overflow: hidden;
        }

        .submenu_title-text {
            flex: 1 1 auto;
            min-width: 0;
            overflow: hidden;
            white-space: nowrap;
            text-overflow: ellipsis;
        }

        .menu-more-wrap {
            opacity: 0;
            transition: opacity 0.2s ease;
            flex-shrink: 0;
        }

        .menu-more {
            display: inline-block;
            font-weight: bold;
            color: var(--td-brand-color);
        }

        .submenu_title--batch {
            margin-left: 4px;
        }

        &.submenu_item_batch {
            padding-left: 0;
        }
    }

    :deep(.submenu_item_batch) {
        cursor: pointer;
        user-select: none;
    }

    .batch-checkbox {
        flex-shrink: 0;
    }

}

.batch-inline-footer {
    position: sticky;
    bottom: 0;
    z-index: 2;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 12px;
    border-top: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);

    .batch-footer-left {
        display: flex;
        align-items: center;
        font-size: 13px;
        color: var(--td-text-color-placeholder);
    }

    .batch-footer-right {
        display: flex;
        align-items: center;
        gap: 6px;
    }
}

/* 知识库下拉菜单样式 */
.kb-dropdown-icon {
    margin-left: auto;
    color: var(--td-text-color-secondary);
    transition: transform 0.3s ease, color 0.2s ease;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;

    &.rotate-180 {
        transform: rotate(180deg);
    }

    &:hover {
        color: var(--td-brand-color);
    }

    &.active {
        color: var(--td-brand-color);
    }

    &.active:hover {
        color: var(--td-brand-color-active);
    }

    svg {
        width: 12px;
        height: 12px;
        transition: inherit;
    }
}

.kb-dropdown-menu {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;
    box-shadow: var(--td-shadow-2);
    z-index: 1000;
    max-height: 200px;
    overflow-y: auto;
}

.kb-dropdown-item {
    padding: 8px 16px;
    cursor: pointer;
    transition: background-color 0.2s ease;
    font-size: 14px;
    color: var(--td-text-color-primary);

    &:hover {
        background-color: var(--td-bg-color-container-hover);
    }

    &.active {
        background-color: var(--td-brand-color-light);
        color: var(--td-brand-color);
        font-weight: 500;
    }

    &:first-child {
        border-radius: 6px 6px 0 0;
    }

    &:last-child {
        border-radius: 0 0 6px 6px;
    }
}

.menu_item-box {
    display: flex;
    align-items: center;
    width: 100%;
    position: relative;
}

/* Empty state when there are no sessions. */
.submenu_empty {
    padding: 24px 14px;
    text-align: center;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    user-select: none;
}

// 顶部 logo_row 右侧的图标按钮组（搜索 + 折叠），与折叠按钮风格一致
.logo_actions {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
}

.header-icon-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    flex-shrink: 0;
    cursor: pointer;
    border-radius: 6px;
    color: var(--td-text-color-secondary);
    transition: background-color 0.2s ease;
    box-sizing: border-box;

    &:hover {
        background: var(--td-bg-color-container-hover);
    }

    .header-icon-img {
        width: 18px;
        height: 18px;
        display: block;
    }
}

// 深色 tooltip 内容：标签 + 浅灰快捷键内联
.cmdk-tip {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    white-space: nowrap;

    .cmdk-tip-label {
        font-size: 13px;
    }

    .cmdk-tip-keys {
        font-size: 13px;
        opacity: 0.6;
        letter-spacing: 0.5px;
    }
}

.menu-pending-badge {
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    margin-left: 6px;
    border-radius: 9px;
    background: rgba(250, 173, 20, 0.2);
    color: var(--td-warning-color);
    font-size: 12px;
    font-weight: 600;
    line-height: 18px;
    text-align: center;
    flex-shrink: 0;
}

.menu_box {
    position: relative;
}
</style>
<style lang="less">
// Dark mode: invert dark logo to light
html[theme-mode="dark"] .aside_box .logo_box .logo {
    filter: invert(1) hue-rotate(180deg);
}

// Dark mode: 滚动条在深色背景下需要更亮的颜色才看得见
html[theme-mode="dark"] .aside_box .menu_top:hover {
    scrollbar-color: rgba(255, 255, 255, 0.22) transparent;
}

html[theme-mode="dark"] .aside_box .menu_top:hover::-webkit-scrollbar-thumb {
    background-color: rgba(255, 255, 255, 0.22);
}

html[theme-mode="dark"] .aside_box .menu_top::-webkit-scrollbar-thumb:hover {
    background-color: rgba(255, 255, 255, 0.38);
}

// Dark mode: invert the top search icon button image to match text color
html[theme-mode="dark"] .aside_box .header-icon-img {
    filter: invert(1);
    opacity: 0.55;
}

html[theme-mode="dark"] .aside_box .header-icon-btn:hover .header-icon-img {
    opacity: 0.9;
}

// Dark mode: make SVG icons match text color (loaded via <img>, currentColor won't work)
html[theme-mode="dark"] .aside_box .menu_icon img.icon {
    filter: invert(1);
    opacity: 0.55;
}

// Hover state: brighter icon like text
html[theme-mode="dark"] .aside_box .menu_item:hover .menu_icon img.icon {
    opacity: 0.9;
}

// menu_item_c_active: text is primary, so icon should match
html[theme-mode="dark"] .aside_box .menu_item_c_active .menu_icon img.icon {
    opacity: 0.9;
}

// Active (green) icons should not be inverted
html[theme-mode="dark"] .aside_box .menu_item_active .menu_icon img.icon {
    filter: none;
    opacity: 1;
}

// 下拉菜单样式已统一至 @/assets/dropdown-menu.less

// 退出登录确认框样式
:deep(.t-popconfirm) {
    .t-popconfirm__content {
        background: var(--td-bg-color-container);
        border: 1px solid var(--td-component-stroke);
        border-radius: 6px;
        box-shadow: var(--td-shadow-3);
        padding: 12px 16px;
        font-size: 14px;
        color: var(--td-text-color-primary);
        max-width: 200px;
    }

    .t-popconfirm__arrow {
        border-bottom-color: var(--td-component-stroke);
    }

    .t-popconfirm__arrow::after {
        border-bottom-color: var(--td-bg-color-container);
    }

    .t-popconfirm__buttons {
        margin-top: 8px;
        display: flex;
        justify-content: flex-end;
        gap: 8px;
    }

    .t-button--variant-outline {
        border-color: var(--td-component-border);
        color: var(--td-text-color-secondary);
    }

    .t-button--theme-danger {
        background-color: var(--td-error-color);
        border-color: var(--td-error-color);
    }

    .t-button--theme-danger:hover {
        background-color: var(--td-error-color);
        border-color: var(--td-error-color);
    }
}
</style>