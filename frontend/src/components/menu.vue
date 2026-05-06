<template>
    <div class="aside_box" :class="{ 'aside_box--collapsed': uiStore.sidebarCollapsed }">
        <!-- 展开时：Logo + 折叠按钮同行 -->
        <div class="logo_row" v-if="!uiStore.sidebarCollapsed">
            <div class="logo_box" @click="router.push('/platform/knowledge-bases')" style="cursor: pointer;">
                <img class="logo" src="@/assets/img/weknora.png" alt="">
                <sup v-if="isLiteEdition" class="lite-badge">Lite</sup>
            </div>
            <div class="sidebar-toggle"
                 @click="uiStore.toggleSidebar"
                 :title="t('menu.collapseSidebar')">
                <svg viewBox="0 0 20 20" width="20" height="20" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor" stroke-width="1.2" />
                    <line x1="7.5" y1="1.5" x2="7.5" y2="18.5" stroke="currentColor" stroke-width="1.2" />
                    <line x1="4" y1="7.5" x2="4" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
                </svg>
            </div>
        </div>
        <!-- 折叠时：展开按钮 -->
        <t-tooltip v-else :content="t('menu.expandSidebar')" placement="right">
            <div class="menu_item sidebar-toggle-item" @click="uiStore.toggleSidebar">
                <div class="menu_item-box">
                    <div class="menu_icon">
                        <svg class="icon" viewBox="0 0 20 20" width="20" height="20" fill="none" xmlns="http://www.w3.org/2000/svg">
                            <rect x="1.5" y="1.5" width="17" height="17" rx="3" stroke="currentColor" stroke-width="1.2" />
                            <line x1="7.5" y1="1.5" x2="7.5" y2="18.5" stroke="currentColor" stroke-width="1.2" />
                            <line x1="5" y1="10" x2="3" y2="8" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
                            <line x1="5" y1="10" x2="3" y2="12" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
                        </svg>
                    </div>
                </div>
            </div>
        </t-tooltip>
        
        <!-- 租户选择器：仅在用户可切换租户时显示 -->
        <TenantSelector v-if="canAccessAllTenants && !uiStore.sidebarCollapsed" />

        <!-- 折叠时右侧拖拽展开手柄 -->
        <div v-if="uiStore.sidebarCollapsed"
             class="sidebar-drag-handle"
             @mousedown="onDragHandleMouseDown" />
        
        <!-- 上半部分：知识库和对话 -->
        <div class="menu_top">
            <!-- 全局搜索入口：点击打开命令面板（⌘K）。放在一级导航最上方，
                 展开态展示快捷键提示，折叠态仅图标 + tooltip。 -->
            <div class="menu_box menu_box--cmdk">
                <t-tooltip :content="cmdkTooltip" placement="right" :disabled="!uiStore.sidebarCollapsed">
                    <div class="menu_item menu_item--cmdk" @click="commandPaletteStore.openPalette('')">
                        <div class="menu_item-box">
                            <div class="menu_icon">
                                <img class="icon" :src="getImgSrc('search.svg')" alt="">
                            </div>
                            <template v-if="!uiStore.sidebarCollapsed">
                                <span class="menu_title">{{ t('menu.search') }}</span>
                                <span class="menu-cmdk-hint" aria-hidden="true">
                                    <kbd>{{ cmdModKeyLabel }}</kbd><kbd>K</kbd>
                                </span>
                            </template>
                        </div>
                    </div>
                </t-tooltip>
            </div>
            <div class="menu_box" :class="{ 'has-submenu': item.children }" v-for="(item, index) in topMenuItems" :key="index">
                <t-tooltip :content="item.title" placement="right" :disabled="!uiStore.sidebarCollapsed">
                <div @click="handleMenuClick(item.path)"
                    @mouseenter="mouseenteMenu(item.path)" @mouseleave="mouseleaveMenu(item.path)"
                     :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : isMenuItemActive(item.path) ? 'menu_item_active' : '']">
                    <div class="menu_item-box">
                        <div class="menu_icon">
                            <img class="icon" :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon : item.icon == 'agent' ? agentIcon : item.icon == 'organization' ? organizationIcon : item.icon == 'logout' ? logoutIcon : item.icon == 'setting' ? settingIcon : prefixIcon)" alt="">
                        </div>
                        <template v-if="!uiStore.sidebarCollapsed">
                            <span class="menu_title" :title="item.title">{{ item.title }}</span>
                            <span v-if="item.path === 'organizations' && orgStore.totalPendingJoinRequestCount > 0" class="menu-pending-badge" :title="t('organization.settings.pendingJoinRequestsBadge')">{{ orgStore.totalPendingJoinRequestCount }}</span>
                            <span v-if="item.path === 'creatChat' && batchMode" class="batch-cancel-hint" @click.stop="exitBatchMode">{{ t('batchManage.cancel') }}</span>
                            <t-icon v-else-if="item.path === 'creatChat'" name="add" class="menu-create-hint" />
                        </template>
                    </div>
                </div>
                </t-tooltip>
                <div ref="submenuscrollContainer" @scroll="handleScroll" class="submenu" v-if="item.children && !uiStore.sidebarCollapsed">
                    <!-- 骨架屏占位 -->
                    <template v-if="loading && groupedSessions.length === 0">
                        <div v-for="n in 5" :key="'skel-'+n" class="submenu_item_p">
                            <div class="submenu_item">
                                <t-skeleton animation="gradient" style="margin-left:18px;width:80%" :row-col="[{ width: '100%', height: '16px' }]" />
                            </div>
                        </div>
                    </template>
                    <template v-for="(group, groupIndex) in groupedSessions" :key="groupIndex">
                        <div class="timeline_header">{{ group.label }}</div>
                        <div class="submenu_item_p" v-for="(subitem, subindex) in group.items" :key="subitem.id">
                            <div :class="['submenu_item', !batchMode && currentSecondpath == subitem.path ? 'submenu_item_active' : '', batchMode && batchSelectedIds.includes(subitem.id) ? 'submenu_item_selected' : '', batchMode ? 'submenu_item_batch' : '']"
                                @mouseenter="mouseenteBotDownr(subitem.id)" @mouseleave="mouseleaveBotDown"
                                @click="batchMode ? toggleBatchSelect(subitem.id) : gotopage(subitem.path)">
                                <t-checkbox v-if="batchMode"
                                    class="batch-checkbox"
                                    :checked="batchSelectedIds.includes(subitem.id)"
                                    @click.stop
                                    @change="toggleBatchSelect(subitem.id)"
                                />
                                <span class="submenu_title"
                                    :style="batchMode ? 'margin-left:4px;max-width:170px;' : (currentSecondpath == subitem.path ? 'margin-left:18px;max-width:160px;' : 'margin-left:18px;max-width:185px;')">
                                    <t-icon v-if="subitem.is_pinned" name="pin" class="submenu_pin_icon" :title="t('menu.pinned')" />
                                    <img v-if="subitem.im_platform && platformLogo(subitem.im_platform)"
                                        :src="platformLogo(subitem.im_platform)"
                                        :alt="subitem.im_platform"
                                        :title="subitem.im_platform"
                                        class="submenu_source_icon" />
                                    {{ subitem.title }}
                                </span>
                                <t-dropdown v-if="!batchMode"
                                    :options="buildSessionMenuOptions(subitem)"
                                    @click="handleSessionMenuClick($event, subitem.originalIndex, subitem)"
                                    placement="bottom-right"
                                    trigger="click">
                                    <div @click.stop class="menu-more-wrap">
                                        <t-icon name="ellipsis" class="menu-more" />
                                    </div>
                                </t-dropdown>
                            </div>
                        </div>
                    </template>
                </div>
                <div v-if="batchMode && item.path === 'creatChat' && !uiStore.sidebarCollapsed" class="batch-inline-footer">
                    <div class="batch-footer-left">
                        <t-checkbox
                            :checked="isAllBatchSelected"
                            :indeterminate="isBatchIndeterminate"
                            @change="toggleBatchSelectAll"
                        >
                            {{ t('batchManage.selectAll') }}
                        </t-checkbox>
                    </div>
                    <t-button
                        size="small"
                        theme="danger"
                        variant="base"
                        :disabled="batchSelectedIds.length === 0"
                        :loading="batchDeleting"
                        @click="handleInlineBatchDelete"
                    >
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
import { onMounted, watch, computed, ref, h } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getSessionsList, delSession, batchDelSessions, deleteAllSessions, clearSessionMessages, pinSession, unpinSession } from "@/api/chat/index";
import { getKnowledgeBaseById } from '@/api/knowledge-base';
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
const cmdkTooltip = computed(() => `${t('menu.search')} · ${cmdModKeyLabel} K`);
const route = useRoute();
const router = useRouter();
const currentpath = ref('');
const currentPage = ref(1);
const page_size = ref(30);
const total = ref(0);
const currentSecondpath = ref('');
const submenuscrollContainer = ref(null);
// 计算总页数
const totalPages = computed(() => Math.ceil(total.value / page_size.value));
const hasMore = computed(() => currentPage.value < totalPages.value);
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

// 时间分组函数
const getTimeCategory = (dateStr: string): string => {
    if (!dateStr) return t('time.earlier');
    
    const date = new Date(dateStr);
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const yesterday = new Date(today.getTime() - 24 * 60 * 60 * 1000);
    const sevenDaysAgo = new Date(today.getTime() - 7 * 24 * 60 * 60 * 1000);
    const thirtyDaysAgo = new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000);
    const oneYearAgo = new Date(today.getTime() - 365 * 24 * 60 * 60 * 1000);
    
    const sessionDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
    
    if (sessionDate.getTime() >= today.getTime()) {
        return t('time.today');
    } else if (sessionDate.getTime() >= yesterday.getTime()) {
        return t('time.yesterday');
    } else if (date.getTime() >= sevenDaysAgo.getTime()) {
        return t('time.last7Days');
    } else if (date.getTime() >= thirtyDaysAgo.getTime()) {
        return t('time.last30Days');
    } else if (date.getTime() >= oneYearAgo.getTime()) {
        return t('time.lastYear');
    } else {
        return t('time.earlier');
    }
};

// 按时间分组Session列表，置顶会话单独置于最上方
const groupedSessions = computed(() => {
    const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item: MenuItem) => item.path === 'creatChat');
    if (!chatMenu || !chatMenu.children || chatMenu.children.length === 0) {
        return [];
    }

    const pinnedLabel = t('time.pinned');
    const groups: { [key: string]: any[] } = {
        [pinnedLabel]: [],
        [t('time.today')]: [],
        [t('time.yesterday')]: [],
        [t('time.last7Days')]: [],
        [t('time.last30Days')]: [],
        [t('time.lastYear')]: [],
        [t('time.earlier')]: []
    };

    (chatMenu.children as any[]).forEach((session: any, index: number) => {
        const withIndex = { ...session, originalIndex: index };
        if (session.is_pinned) {
            groups[pinnedLabel].push(withIndex);
            return;
        }
        const category = getTimeCategory(session.updated_at || session.created_at);
        groups[category].push(withIndex);
    });

    // 按顺序返回非空分组（置顶组在最上方）
    const orderedLabels = [pinnedLabel, t('time.today'), t('time.yesterday'), t('time.last7Days'), t('time.last30Days'), t('time.lastYear'), t('time.earlier')];
    return orderedLabels
        .filter(label => groups[label].length > 0)
        .map(label => ({
            label,
            items: groups[label]
        }));
});

const loading = ref(false)
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
                    const chatMenuItem = (menuArr.value as any[]).find((m: any) => m.path === 'creatChat');
                    if (isDeleteAll) {
                        if (chatMenuItem) chatMenuItem.children = [];
                        total.value = 0;
                    } else {
                        const ids = [...batchSelectedIds.value]
                        if (chatMenuItem && chatMenuItem.children) {
                            for (const id of ids) {
                                const idx = chatMenuItem.children.findIndex((s: any) => s.id === id);
                                if (idx !== -1) chatMenuItem.children.splice(idx, 1);
                            }
                        }
                        total.value = Math.max(0, total.value - ids.length);
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

const handleSessionMenuClick = (data: { value: string }, index: number, item: any) => {
    if (data?.value === 'delete') {
        delCard(index, item);
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

const togglePin = (item: any, pin: boolean) => {
    if (pinningIds.value.has(item.id)) return;
    pinningIds.value.add(item.id);

    const call = pin ? pinSession(item.id) : unpinSession(item.id);
    call.then((res: any) => {
        if (res && res.success) {
            // 乐观更新本地列表项，避免整表重拉引起抖动。
            const chatMenu = (menuArr.value as any[]).find((m: any) => m.path === 'creatChat');
            const idx = chatMenu?.children?.findIndex((s: any) => s.id === item.id) ?? -1;
            if (idx >= 0) {
                const target = chatMenu.children[idx];
                target.is_pinned = pin;
                target.pinned_at = pin ? new Date().toISOString() : null;
                // 置顶时把元素挪到数组最前，确保在置顶分组中出现在最上方
                // （groupedSessions 按 children 顺序分组）。取消置顶时无需移动，
                // 元素会自然回到它在时间分组内的原位。
                if (pin && idx > 0) {
                    chatMenu.children.splice(idx, 1);
                    chatMenu.children.unshift(target);
                }
            }
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

const delCard = (index: number, item: any) => {
    delSession(item.id).then((res: any) => {
        if (res && (res as any).success) {
            // 找到 'creatChat' 菜单项
            const chatMenuItem = (menuArr.value as any[]).find((m: any) => m.path === 'creatChat');
            
            if (chatMenuItem && chatMenuItem.children) {
                const children = chatMenuItem.children;
                // 通过ID查找索引，比依赖传入的index更安全
                const actualIndex = children.findIndex((s: any) => s.id === item.id);
                
                if (actualIndex !== -1) {
                    children.splice(actualIndex, 1);
                }
            }
            
            if (item.id == route.params.chatid) {
                // 删除当前会话后，跳转到全局创建聊天页面
                router.push('/platform/creatChat');
            }
            // 更新总数
            if (total.value > 0) {
                total.value--;
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
// 滚动处理
const checkScrollBottom = () => {
    const container = submenuscrollContainer.value
    if (!container || !container[0]) return

    const { scrollTop, scrollHeight, clientHeight } = container[0]
    const isBottom = scrollHeight - (scrollTop + clientHeight) < 100 // 触底阈值
    
    if (isBottom && hasMore.value && !loading.value) {
        currentPage.value++;
        getMessageList(true);
    }
}
const handleScroll = debounce(checkScrollBottom, 200)
const getMessageList = async (isLoadMore = false) => {
    if (loading.value) return Promise.resolve();
    loading.value = true;

    // 只有在首次加载或路由变化时才清空数组，滚动加载时不清空
    if (!isLoadMore) {
        currentPage.value = 1; // 重置页码
        usemenuStore.clearMenuArr();
    }

    return getSessionsList(currentPage.value, page_size.value).then((res: any) => {
        if (res.data && res.data.length) {
            // Display all sessions globally without filtering
            res.data.forEach((item: any) => {
                let obj = {
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
                }
                usemenuStore.updatemenuArr(obj)
            });
        }
        if ((res as any).total) {
            total.value = (res as any).total;
        }
        loading.value = false;
    }).catch(() => {
        loading.value = false;
    })
}

onMounted(async () => {
    const routeName = typeof route.name === 'string' ? route.name : (route.name ? String(route.name) : '')
    currentpath.value = routeName;
    if (route.params.chatid) {
        currentSecondpath.value = `chat/${route.params.chatid}`;
    }

    isLiteEdition.value = authStore.isLiteMode
    getSystemInfo().then(res => {
        if (res.data?.edition === 'lite') {
            isLiteEdition.value = true
            authStore.setLiteMode(true)
        }
    }).catch(() => {})
    
    // 初始化知识库信息
    const kbId = (route.params as any)?.kbId as string
    if (kbId && isInKnowledgeBase.value) {
        try {
            const kbRes: any = await getKnowledgeBaseById(kbId)
            if (kbRes?.data) {
                currentKbName.value = kbRes.data.name || ''
                currentKbInfo.value = kbRes.data
            }
        } catch {}
    } else {
        currentKbName.value = ''
        currentKbInfo.value = null
    }
    
    // 加载对话列表
    getMessageList();
    // 若组织列表未加载则拉取一次，用于侧栏「待审批」角标
    if (orgStore.organizations.length === 0) {
        orgStore.fetchOrganizations();
    }
});

watch([() => route.name, () => route.params], (newvalue, oldvalue) => {
    const nameStr = typeof newvalue[0] === 'string' ? (newvalue[0] as string) : (newvalue[0] ? String(newvalue[0]) : '')
    currentpath.value = nameStr;
    if (newvalue[1].chatid) {
        currentSecondpath.value = `chat/${newvalue[1].chatid}`;
    } else {
        currentSecondpath.value = "";
    }
    
    // 只在必要时刷新对话列表，避免不必要的重新加载导致列表抖动
    // 需要刷新的情况：
    // 1. 创建新会话后（从 creatChat/kbCreatChat 跳转到 chat/:id）
    // 2. 删除会话后已在 delCard 中处理，不需要在这里刷新
    const oldRouteNameStr = typeof oldvalue?.[0] === 'string' ? (oldvalue[0] as string) : (oldvalue?.[0] ? String(oldvalue[0]) : '')
    const isCreatingNewSession = (oldRouteNameStr === 'globalCreatChat' || oldRouteNameStr === 'kbCreatChat') && 
                                 nameStr !== 'globalCreatChat' && nameStr !== 'kbCreatChat';
    
    // 只在创建新会话时才刷新列表
    if (isCreatingNewSession) {
        getMessageList();
    }
    
    // 路由变化时更新图标状态和知识库信息（不涉及对话列表）
    getIcon(nameStr);
    
    // 如果切换了知识库，更新知识库名称但不重新加载对话列表
    if (newvalue[1].kbId !== oldvalue?.[1]?.kbId) {
        const kbId = (newvalue[1] as any)?.kbId as string;
        if (kbId && isInKnowledgeBase.value) {
            getKnowledgeBaseById(kbId).then((kbRes: any) => {
                if (kbRes?.data) {
                    currentKbName.value = kbRes.data.name || '';
                    currentKbInfo.value = kbRes.data;
                }
            }).catch(() => {
                currentKbInfo.value = null;
            });
        } else {
            currentKbName.value = '';
            currentKbInfo.value = null;
        }
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
    min-width: 260px;
    width: 260px;
    padding: 8px;
    background: var(--td-bg-color-sidebar);
    box-sizing: border-box;
    height: 100vh;
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
        padding: 8px 4px;
        overflow: visible;

        .menu_item {
            justify-content: center;
            padding: 13px 0;
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
    }

    .logo_row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        height: 56px;
        flex-shrink: 0;
        padding: 0 8px 0 16px;
    }

    .sidebar-toggle {
        display: flex;
        align-items: center;
        justify-content: center;
        width: 36px;
        height: 36px;
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

        .logo{
            width: 134px;
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
        overflow: hidden;
        min-height: 0;
    }

    .menu_bottom {
        flex-shrink: 0;
        display: flex;
        flex-direction: column;
    }

    .menu_box {
        display: flex;
        flex-direction: column;
        
        &.has-submenu {
            flex: 1;
            min-height: 0;
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

        .menu-create-hint {
            color: var(--td-brand-color) !important;
            opacity: 1;
        }
    }

    .menu_item_c_active {

        .menu_icon,
        .menu_title {
            color: var(--td-text-color-primary);
        }
    }

    .menu_p {
        height: 56px;
        padding: 6px 0;
        box-sizing: border-box;
    }

    .menu_item {
        cursor: pointer;
        display: flex;
        align-items: center;
        justify-content: space-between;
        height: 48px;
        padding: 13px 8px 13px 16px;
        box-sizing: border-box;
        margin-bottom: 4px;
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
        margin-right: 10px;
        color: var(--td-text-color-secondary);

        .icon {
            width: 20px;
            height: 20px;
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
        line-height: 22px;
        overflow: hidden;
        white-space: nowrap;
        max-width: 120px;
        flex: 1;
    }

    .submenu {
        font-family: var(--app-font-family);
        font-size: 14px;
        font-style: normal;
        overflow-y: auto;
        scrollbar-width: none;
        flex: 1;
        min-height: 0;
        margin-left: 4px;
    }

    .submenu_pin_icon {
        color: inherit;
        font-size: 12px;
        margin-right: 4px;
        vertical-align: middle;
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

    .submenu_item:hover .submenu_source_icon,
    .submenu_item_active .submenu_source_icon {
        filter: none;
        opacity: 1;
    }
    
    @keyframes menuItemFadeIn {
        from { opacity: 0; transform: translateX(-4px); }
        to { opacity: 1; transform: translateX(0); }
    }

    .timeline_header {
        font-family: var(--app-font-family);
        font-size: 12px;
        font-weight: 600;
        color: var(--td-text-color-disabled);
        padding: 12px 18px 6px 18px;
        margin-top: 8px;
        line-height: 20px;
        user-select: none;
        animation: menuItemFadeIn 0.25s ease-out;
        
        &:first-child {
            margin-top: 4px;
        }
    }

    .submenu_item_p {
        height: 44px;
        padding: 4px 0px 4px 0px;
        box-sizing: border-box;
        animation: menuItemFadeIn 0.25s ease-out;
    }


    .submenu_item {
        cursor: pointer;
        display: flex;
        align-items: center;
        color: var(--td-text-color-primary);
        font-weight: 400;
        line-height: 22px;
        height: 36px;
        padding-left: 0px;
        padding-right: 14px;
        position: relative;

        .submenu_title {
            overflow: hidden;
            white-space: nowrap;
            text-overflow: ellipsis;
        }

        .menu-more-wrap {
            margin-left: auto;
            opacity: 0;
            transition: opacity 0.2s ease;
        }

        .menu-more {
            display: inline-block;
            font-weight: bold;
            color: var(--td-brand-color);
        }

        .sub_title {
            margin-left: 14px;
        }

        &:hover {
            background: var(--td-bg-color-container-hover);
            color: var(--td-text-color-primary);
            border-radius: 8px;

            .menu-more {
                color: var(--td-text-color-primary);
            }

            .menu-more-wrap {
                opacity: 1;
            }

            .submenu_title {
                max-width: 160px !important;

            }
        }
    }

    .submenu_item_active {
        background: var(--td-brand-color-light) !important;
        color: var(--td-brand-color) !important;
        border-radius: 8px;

        .menu-more {
            color: var(--td-brand-color) !important;
        }

        .menu-more-wrap {
            opacity: 1;
        }

        .submenu_title {
            max-width: 160px !important;
        }
    }

    .submenu_item_batch {
        padding-left: 10px;
        cursor: pointer;
        user-select: none;
    }

    .submenu_item_selected {
        background: rgba(7, 192, 95, 0.05) !important;
        border-radius: 8px;
    }

    .batch-checkbox {
        flex-shrink: 0;
    }
}

.batch-cancel-hint {
    margin-left: auto;
    margin-right: 8px;
    font-size: 13px;
    color: var(--td-text-color-disabled);
    cursor: pointer;
    flex-shrink: 0;
    transition: color 0.2s ease;
    font-weight: 400;

    &:hover {
        color: var(--td-text-color-primary);
    }
}

.batch-inline-footer {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 14px;
    border-top: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);

    .batch-footer-left {
        display: flex;
        align-items: center;
        font-size: 13px;
        color: var(--td-text-color-placeholder);
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

.menu-create-hint {
    margin-left: auto;
    margin-right: 8px;
    font-size: 16px;
    color: var(--td-brand-color);
    opacity: 0.7;
    transition: opacity 0.2s ease;
    flex-shrink: 0;
}

.menu_item:hover .menu-create-hint {
    opacity: 1;
}

.menu-cmdk-hint {
    margin-left: auto;
    margin-right: 8px;
    display: inline-flex;
    align-items: center;
    gap: 2px;
    flex-shrink: 0;
    opacity: 0.7;
    transition: opacity 0.2s ease;

    kbd {
        display: inline-block;
        padding: 0 4px;
        min-width: 14px;
        font-size: 10px;
        font-family: inherit;
        line-height: 14px;
        text-align: center;
        background: var(--td-bg-color-secondarycontainer);
        border: 1px solid var(--td-component-stroke);
        border-radius: 3px;
        color: var(--td-text-color-secondary);
    }
}

.menu_item--cmdk:hover .menu-cmdk-hint {
    opacity: 1;
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