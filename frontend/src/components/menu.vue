<template>
    <div class="aside_box">
        <div class="logo_box" @click="router.push('/platform/knowledge-bases')" style="cursor: pointer;">
            <img class="logo" src="@/assets/img/weknora.png" alt="">
        </div>
        
        <!-- 租户选择器：仅在用户可切换租户时显示 -->
        <TenantSelector v-if="canAccessAllTenants" />
        
        <!-- 上半部分：知识库和对话 -->
        <div class="menu_top">
            <div v-if="showKbActions" class="kb-action-wrapper">
                <div class="kb-action-label">{{ t('knowledgeBase.quickActions') }}</div>
                <div class="kb-action-menu">
                    <template v-if="showCreateKbAction">
                        <div class="menu_item kb-action-item" @click.stop="handleCreateKnowledgeBase">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M9 3.75V14.25M3.75 9H14.25" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('knowledgeList.create') }}</span>
                            </div>
                        </div>
                    </template>
                    <template v-else-if="showCreateAgentAction">
                        <div class="menu_item kb-action-item" @click.stop="handleCreateAgent">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M9 3.75V14.25M3.75 9H14.25" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('agent.createAgent') }}</span>
                            </div>
                        </div>
                    </template>
                    <template v-else-if="showDocActions">
                        <div class="menu_item kb-action-item" @click.stop="handleDocUploadClick">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M15.75 11.25V14.25C15.75 14.6478 15.592 15.0294 15.3107 15.3107C15.0294 15.592 14.6478 15.75 14.25 15.75H3.75C3.35218 15.75 2.97064 15.592 2.68934 15.3107C2.40804 15.0294 2.25 14.6478 2.25 14.25V11.25M12.75 6L9 2.25M9 2.25L5.25 6M9 2.25V11.25" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('upload.uploadDocument') }}</span>
                            </div>
                        </div>
                        <div class="menu_item kb-action-item" @click.stop="handleDocFolderUploadClick">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 24 24" fill="none">
                                    <path d="M22 19C22 19.5304 21.7893 20.0391 21.4142 20.4142C21.0391 20.7893 20.5304 21 20 21H4C3.46957 21 2.96086 20.7893 2.58579 20.4142C2.21071 20.0391 2 19.5304 2 19V5C2 4.46957 2.21071 3.96086 2.58579 3.58579C2.96086 3.21071 3.46957 3 4 3H9L11 6H20C20.5304 6 21.0391 6.21071 21.4142 6.58579C21.7893 6.96086 22 7.46957 22 8V19Z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                                    <path d="M12 11V17M9 14H15" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('upload.uploadFolder') }}</span>
                            </div>
                        </div>
                        <div class="menu_item kb-action-item" @click.stop="handleDocURLImport">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 24 24" fill="none">
                                    <path d="M10 13C10.4295 13.5741 10.9774 14.0491 11.6066 14.3929C12.2357 14.7367 12.9315 14.9411 13.6467 14.9923C14.3618 15.0435 15.0796 14.9403 15.7513 14.6897C16.4231 14.4392 17.0331 14.047 17.54 13.54L20.54 10.54C21.4508 9.59695 21.9548 8.33394 21.9434 7.02296C21.932 5.71198 21.4061 4.45791 20.4791 3.53087C19.5521 2.60383 18.298 2.07799 16.987 2.0666C15.676 2.0552 14.413 2.55918 13.47 3.46997L11.75 5.17997M14 11C13.5705 10.4258 13.0226 9.95078 12.3934 9.60703C11.7642 9.26327 11.0685 9.05885 10.3533 9.00763C9.63819 8.95641 8.92037 9.0596 8.24861 9.31018C7.57685 9.56077 6.96685 9.9529 6.45996 10.46L3.45996 13.46C2.54917 14.403 2.04519 15.666 2.05659 16.977C2.06798 18.288 2.59382 19.542 3.52086 20.4691C4.44791 21.3961 5.70197 21.9219 7.01295 21.9333C8.32393 21.9447 9.58694 21.4408 10.53 20.53L12.24 18.82" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('knowledgeBase.importURL') }}</span>
                            </div>
                        </div>
                        <div class="menu_item kb-action-item" @click.stop="handleDocManualCreate">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M8.25 2.25H3.75C3.35218 2.25 2.97064 2.40804 2.68934 2.68934C2.40804 2.97064 2.25 3.35218 2.25 3.75V14.25C2.25 14.6478 2.40804 15.0294 2.68934 15.3107C2.97064 15.592 3.35218 15.75 3.75 15.75H14.25C14.6478 15.75 15.0294 15.592 15.3107 15.3107C15.592 15.0294 15.75 14.6478 15.75 14.25V9.75M13.875 3.375L5.625 11.625L5.25 12.75L6.375 12.375L14.625 4.125C14.7745 3.97554 14.8571 3.77516 14.8571 3.5625C14.8571 3.34984 14.7745 3.14946 14.625 3L15 2.625L14.625 3C14.4755 2.85054 14.2752 2.76786 14.0625 2.76786C13.8498 2.76786 13.6495 2.85054 13.5 3L13.875 3.375Z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('upload.onlineEdit') }}</span>
                            </div>
                        </div>
                    </template>
                    <template v-else-if="showFaqActions">
                        <div class="menu_item kb-action-item" @click.stop="handleFaqCreateFromMenu">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M9 3.75V14.25M3.75 9H14.25" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('knowledgeEditor.faq.editorCreate') }}</span>
                            </div>
                        </div>
                        <div class="menu_item kb-action-item" @click.stop="handleFaqImportFromMenu">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M15.75 11.25V14.25C15.75 14.6478 15.592 15.0294 15.3107 15.3107C15.0294 15.592 14.6478 15.75 14.25 15.75H3.75C3.35218 15.75 2.97064 15.592 2.68934 15.3107C2.40804 15.0294 2.25 14.6478 2.25 14.25V11.25M5.25 7.5L9 11.25M9 11.25L12.75 7.5M9 11.25V2.25" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('knowledgeEditor.faqImport.importButton') }}</span>
                            </div>
                        </div>
                        <div class="menu_item kb-action-item" @click.stop="handleFaqSearchTestFromMenu">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M8.25 15C11.9779 15 15 11.9779 15 8.25C15 4.52208 11.9779 1.5 8.25 1.5C4.52208 1.5 1.5 4.52208 1.5 8.25C1.5 11.9779 4.52208 15 8.25 15Z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                    <path d="M16.5 16.5L12.4875 12.4875" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('knowledgeEditor.faq.searchTest') }}</span>
                            </div>
                        </div>
                        <div class="menu_item kb-action-item" @click.stop="handleFaqExportFromMenu">
                            <div class="kb-action-icon-wrapper">
                                <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                    <path d="M15.75 11.25V14.25C15.75 14.6478 15.592 15.0294 15.3107 15.3107C15.0294 15.592 14.6478 15.75 14.25 15.75H3.75C3.35218 15.75 2.97064 15.592 2.68934 15.3107C2.40804 15.0294 2.25 14.6478 2.25 14.25V11.25M12.75 6L9 2.25M9 2.25L5.25 6M9 2.25V11.25" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                </svg>
                            </div>
                            <div class="kb-action-content">
                                <span class="kb-action-title">{{ t('knowledgeEditor.faqExport.exportButton') }}</span>
                            </div>
                        </div>
                        <t-dropdown
                          v-if="selectedFaqCount > 0"
                          :options="faqBatchActionOptions"
                          trigger="hover"
                          placement="right"
                          @click="handleFaqBatchActionFromMenu"
                        >
                          <div class="menu_item kb-action-item">
                            <div class="kb-action-icon-wrapper">
                              <svg class="kb-action-icon" width="18" height="18" viewBox="0 0 18 18" fill="none">
                                <path d="M3.75 9H14.25M9 3.75V14.25" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                                <path d="M3.75 3.75H14.25V14.25H3.75V3.75Z" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                              </svg>
                            </div>
                            <div class="kb-action-content">
                              <span class="kb-action-title">{{ t('knowledgeEditor.faq.batchOperations') }}</span>
                              <span class="kb-action-count">({{ selectedFaqCount }})</span>
                            </div>
                          </div>
                        </t-dropdown>
                    </template>
                </div>
            </div>
            <div class="menu_box" :class="{ 'has-submenu': item.children }" v-for="(item, index) in topMenuItems" :key="index">
                <div @click="handleMenuClick(item.path)"
                    @mouseenter="mouseenteMenu(item.path)" @mouseleave="mouseleaveMenu(item.path)"
                     :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : isMenuItemActive(item.path) ? 'menu_item_active' : '']">
                    <div class="menu_item-box">
                        <div class="menu_icon">
                            <img class="icon" :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon : item.icon == 'agent' ? agentIcon : item.icon == 'logout' ? logoutIcon : item.icon == 'setting' ? settingIcon : prefixIcon)" alt="">
                        </div>
                        <span class="menu_title" :title="item.title">{{ item.title }}</span>
                        <t-icon v-if="item.path === 'creatChat'" name="add" class="menu-create-hint" />
                    </div>
                </div>
                <div ref="submenuscrollContainer" @scroll="handleScroll" class="submenu" v-if="item.children">
                    <template v-for="(group, groupIndex) in groupedSessions" :key="groupIndex">
                        <div class="timeline_header">{{ group.label }}</div>
                        <div class="submenu_item_p" v-for="(subitem, subindex) in group.items" :key="subitem.id">
                            <div :class="['submenu_item', currentSecondpath == subitem.path ? 'submenu_item_active' : '']"
                                @mouseenter="mouseenteBotDownr(subitem.id)" @mouseleave="mouseleaveBotDown"
                                @click="gotopage(subitem.path)">
                                <span class="submenu_title"
                                    :style="currentSecondpath == subitem.path ? 'margin-left:18px;max-width:160px;' : 'margin-left:18px;max-width:185px;'">
                                    {{ subitem.title }}
                                </span>
                                <t-dropdown 
                                    :options="[{ content: t('upload.deleteRecord'), value: 'delete' }]"
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
            </div>
        </div>
        
        
        <!-- 下半部分：用户菜单 -->
        <div class="menu_bottom">
            <UserMenu />
        </div>
        
        <input
            ref="docUploadInput"
            type="file"
            class="kb-upload-input"
            accept=".pdf,.docx,.doc,.txt,.md,.jpg,.jpeg,.png,.csv,.xls,.xlsx"
            multiple
            @change="handleDocFileChange"
        />
        <input
            ref="docFolderInput"
            type="file"
            class="kb-upload-input"
            webkitdirectory
            @change="handleDocFolderChange"
        />
    </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia';
import { onMounted, onUnmounted, watch, computed, ref, reactive } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getSessionsList, delSession } from "@/api/chat/index";
import { getKnowledgeBaseById, uploadKnowledgeFile, createKnowledgeFromURL } from '@/api/knowledge-base';
import { logout as logoutApi } from '@/api/auth';
import { useMenuStore } from '@/stores/menu';
import { useAuthStore } from '@/stores/auth';
import { useUIStore } from '@/stores/ui';
import { MessagePlugin } from "tdesign-vue-next";
import UserMenu from '@/components/UserMenu.vue';
import TenantSelector from '@/components/TenantSelector.vue';
import { useI18n } from 'vue-i18n';
import { kbFileTypeVerification } from '@/utils';

const { t } = useI18n();
const usemenuStore = useMenuStore();
const authStore = useAuthStore();
const uiStore = useUIStore();
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
const { menuArr } = storeToRefs(usemenuStore);
let activeSubmenu = ref<string>('');

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

// 分离上下两部分菜单
const topMenuItems = computed<MenuItem[]>(() => {
    return (menuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => 
        item.path === 'knowledge-bases' || item.path === 'agents' || item.path === 'creatChat'
    );
});

const bottomMenuItems = computed<MenuItem[]>(() => {
    return (menuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => {
        if (item.path === 'knowledge-bases' || item.path === 'agents' || item.path === 'creatChat') {
            return false;
        }
        return true;
    });
});

// 当前知识库信息
const currentKbName = ref<string>('')
const currentKbInfo = ref<any>(null)
const docUploadInput = ref<HTMLInputElement | null>(null)
const docFolderInput = ref<HTMLInputElement | null>(null)
const pendingUploadKbId = ref<string | null>(null)
const selectedFaqCount = ref<number>(0)
const selectedFaqEnabledCount = ref<number>(0)
const selectedFaqDisabledCount = ref<number>(0)

// 监听FAQ选中数量变化
const handleFaqSelectionChanged = ((event: CustomEvent<{ count: number; enabledCount?: number; disabledCount?: number }>) => {
  const count = event.detail?.count || 0
  selectedFaqCount.value = count
  selectedFaqEnabledCount.value = event.detail?.enabledCount || 0
  selectedFaqDisabledCount.value = event.detail?.disabledCount || 0
}) as EventListener

const showKbActions = computed(() => 
    (isInKnowledgeBase.value && !!currentKbInfo.value) || 
    isInKnowledgeBaseList.value || 
    isInCreatChat.value ||
    isInChatDetail.value ||
    isInAgentList.value
)
const currentKbType = computed(() => currentKbInfo.value?.type || 'document')
const showDocActions = computed(() => showKbActions.value && isInKnowledgeBase.value && currentKbType.value !== 'faq')
const showFaqActions = computed(() => showKbActions.value && isInKnowledgeBase.value && currentKbType.value === 'faq')
const showCreateKbAction = computed(() => showKbActions.value && (isInKnowledgeBaseList.value || isInCreatChat.value || isInChatDetail.value))
const showCreateAgentAction = computed(() => showKbActions.value && isInAgentList.value)

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

// 按时间分组Session列表
const groupedSessions = computed(() => {
    const chatMenu = (menuArr.value as unknown as MenuItem[]).find((item: MenuItem) => item.path === 'creatChat');
    if (!chatMenu || !chatMenu.children || chatMenu.children.length === 0) {
        return [];
    }
    
    const groups: { [key: string]: any[] } = {
        [t('time.today')]: [],
        [t('time.yesterday')]: [],
        [t('time.last7Days')]: [],
        [t('time.last30Days')]: [],
        [t('time.lastYear')]: [],
        [t('time.earlier')]: []
    };
    
    // 将sessions按时间分组
    (chatMenu.children as any[]).forEach((session: any, index: number) => {
        const category = getTimeCategory(session.updated_at || session.created_at);
        groups[category].push({
            ...session,
            originalIndex: index
        });
    });
    
    // 按顺序返回非空分组
    const orderedLabels = [t('time.today'), t('time.yesterday'), t('time.last7Days'), t('time.last30Days'), t('time.lastYear'), t('time.earlier')];
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

const handleSessionMenuClick = (data: { value: string }, index: number, item: any) => {
    if (data?.value === 'delete') {
        delCard(index, item);
    }
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
            MessagePlugin.error("删除失败，请稍后再试!");
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
                    title: item.title ? item.title : "新会话", 
                    path: `chat/${item.id}`, 
                    id: item.id, 
                    isMore: false, 
                    isNoTitle: item.title ? false : true,
                    created_at: item.created_at,
                    updated_at: item.updated_at
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
    
    // 监听FAQ选中数量变化
    window.addEventListener('faqSelectionChanged', handleFaqSelectionChanged)
});

onUnmounted(() => {
    window.removeEventListener('faqSelectionChanged', handleFaqSelectionChanged)
})

watch([() => route.name, () => route.params], (newvalue, oldvalue) => {
    // 切换知识库时重置选中数量
    if (newvalue[1].kbId !== oldvalue?.[1]?.kbId) {
        selectedFaqCount.value = 0
    }
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
let settingIcon = ref('setting.svg'); // 设置图标
let agentIcon = ref('agent.svg'); // 智能体图标
let pathPrefix = ref(route.name)
  const getIcon = (path: string) => {
      // 根据当前路由状态更新所有图标
      const kbActiveState = getIconActiveState('knowledge-bases');
      const creatChatActiveState = getIconActiveState('creatChat');
      const settingsActiveState = getIconActiveState('settings');
      const agentsActiveState = route.name === 'agentList';
      
      // 知识库图标：只在知识库页面显示绿色
      knowledgeIcon.value = kbActiveState.isKbActive ? 'zhishiku-green.svg' : 'zhishiku.svg';
      
      // 智能体图标：只在智能体页面显示绿色
      agentIcon.value = agentsActiveState ? 'agent-green.svg' : 'agent.svg';
      
      // 对话图标：只在对话创建页面显示绿色，在知识库页面显示灰色，其他情况显示默认
      prefixIcon.value = creatChatActiveState.isCreatChatActive ? 'prefixIcon-green.svg' : 
                        kbActiveState.isKbActive ? 'prefixIcon-grey.svg' : 
                        'prefixIcon.svg';
      
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
        // 智能体菜单项：跳转到智能体列表
        router.push('/platform/agents')
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
        MessagePlugin.success('已退出登录');
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
    if (pathPrefix.value != 'knowledge-bases' && pathPrefix.value != 'creatChat' && path != 'knowledge-bases') {
        prefixIcon.value = 'prefixIcon-grey.svg';
    }
}
const mouseleaveMenu = (path: string) => {
    if (pathPrefix.value != 'knowledge-bases' && pathPrefix.value != 'creatChat' && path != 'knowledge-bases') {
        const nameStr = typeof route.name === 'string' ? route.name as string : (route.name ? String(route.name) : '')
        getIcon(nameStr)
    }
}

const ensureDocKnowledgeBaseReady = async (): Promise<string | null> => {
    const kbId = await getCurrentKbId()
    if (!kbId) {
        MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
        return null
    }
    if (currentKbType.value === 'faq') {
        MessagePlugin.warning(t('knowledgeBase.docActionUnsupported'))
        return null
    }
    if (!currentKbInfo.value || !currentKbInfo.value.embedding_model_id || !currentKbInfo.value.summary_model_id) {
        MessagePlugin.warning(t('knowledgeBase.notInitialized'))
        return null
    }
    return kbId
}

const handleDocUploadClick = async () => {
    const kbId = await ensureDocKnowledgeBaseReady()
    if (!kbId) return
    pendingUploadKbId.value = kbId
    docUploadInput.value?.click()
}

const FAILED_FILES_PREVIEW_LIMIT = 10

const summarizeFailedFiles = (failedFiles: Array<{ name: string; reason: string }>) => {
    const duplicateLabel = t('knowledgeBase.fileExists')
    let duplicateCount = 0
    const nonDuplicate: Array<{ name: string; reason: string }> = []
    failedFiles.forEach((file) => {
        if (file.reason === duplicateLabel) {
            duplicateCount++
        } else {
            nonDuplicate.push(file)
        }
    })

    const previewList = nonDuplicate.slice(0, FAILED_FILES_PREVIEW_LIMIT).map(f => `• ${f.name}: ${f.reason}`)
    let nonDuplicateText = ''
    if (previewList.length) {
        nonDuplicateText = previewList.join('\n')
        if (nonDuplicate.length > FAILED_FILES_PREVIEW_LIMIT) {
            nonDuplicateText += `\n${t('knowledgeBase.andMoreFiles', { count: nonDuplicate.length - FAILED_FILES_PREVIEW_LIMIT })}`
        }
    }

    return {
        duplicateCount,
        nonDuplicateText,
    }
}

const handleDocFileChange = async (event: Event) => {
    const input = event.target as HTMLInputElement
    const files = input?.files
    if (!files || files.length === 0) {
        pendingUploadKbId.value = null
        return
    }

    const kbId = pendingUploadKbId.value || (await ensureDocKnowledgeBaseReady())
    pendingUploadKbId.value = null
    if (!kbId) {
        input.value = ''
        return
    }

    // 过滤有效文件
    const validFiles: File[] = []
    let invalidCount = 0
    const isSingleFile = files.length === 1

    for (let i = 0; i < files.length; i++) {
        const file = files[i]
        // 单文件时显示错误，多文件时静默过滤
        if (kbFileTypeVerification(file, !isSingleFile)) {
            invalidCount++
        } else {
            validFiles.push(file)
        }
    }

    // 如果没有有效文件，多文件时显示汇总提示
    if (validFiles.length === 0) {
        if (!isSingleFile && invalidCount > 0) {
            MessagePlugin.error(t('knowledgeBase.noValidFilesSelected'))
        }
        // 单文件的错误已经在 kbFileTypeVerification 中显示了
        input.value = ''
        return
    }

    // 批量上传
    let successCount = 0
    let failCount = 0
    const totalCount = validFiles.length
    const failedFiles: Array<{ name: string; reason: string }> = []

    // 为每个文件创建上传任务并发送事件通知
    const uploadPromises = validFiles.map(async (file) => {
        const uploadId = `${file.name}_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
        let progress = 0
        let status: 'uploading' | 'success' | 'error' = 'uploading'
        let error: string | undefined

        // 发送开始上传事件
        window.dispatchEvent(new CustomEvent('knowledgeFileUploadStart', {
            detail: { 
                kbId, 
                uploadId, 
                fileName: file.name
            }
        }))

        try {
            // 获取当前选中的分类ID
            const tagIdToUpload = uiStore.selectedTagId !== '__untagged__' ? uiStore.selectedTagId : undefined
            await uploadKnowledgeFile(
                kbId, 
                { file, tag_id: tagIdToUpload },
                (progressEvent: any) => {
                    if (progressEvent.total) {
                        progress = Math.round((progressEvent.loaded * 100) / progressEvent.total)
                        // 发送进度更新事件
                        window.dispatchEvent(new CustomEvent('knowledgeFileUploadProgress', {
                            detail: { 
                                kbId, 
                                uploadId, 
                                progress 
                            }
                        }))
                    }
                }
            )
            successCount++
            status = 'success'
            progress = 100
        } catch (error: any) {
            failCount++
            let errorReason = error?.error?.message || error?.message || t('knowledgeBase.uploadFailed')
            if (error?.code === 'duplicate_file' || error?.error?.code === 'duplicate_file') {
                errorReason = t('knowledgeBase.fileExists')
            }
            status = 'error'
            error = errorReason
            failedFiles.push({ name: file.name, reason: errorReason })

            // 只在单文件上传时显示详细错误
            if (totalCount === 1) {
                MessagePlugin.error(errorReason)
            }
        } finally {
            // 发送上传完成事件
            window.dispatchEvent(new CustomEvent('knowledgeFileUploadComplete', {
                detail: { 
                    kbId, 
                    uploadId, 
                    status,
                    progress,
                    error
                }
            }))
        }
    })

    // 等待所有上传完成
    await Promise.allSettled(uploadPromises)

    // 显示上传结果
    if (successCount > 0) {
        window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
            detail: { kbId }
        }))
    }

    if (totalCount === 1) {
        if (successCount === 1) {
            MessagePlugin.success(t('knowledgeBase.uploadSuccess'))
        }
        // 单文件失败时已经在上面显示了详细错误
    } else {
        if (failCount === 0) {
            MessagePlugin.success(t('knowledgeBase.uploadAllSuccess', { count: successCount }))
        } else if (successCount > 0) {
            const { duplicateCount, nonDuplicateText } = summarizeFailedFiles(failedFiles)
            const extraSections: string[] = []
            if (duplicateCount > 0) {
                extraSections.push(t('knowledgeBase.duplicateFilesSkipped', { count: duplicateCount }))
            }
            if (nonDuplicateText) {
                extraSections.push(t('knowledgeBase.failedFilesList') + '\n' + nonDuplicateText)
            }
            const extraContent = extraSections.length ? '\n\n' + extraSections.join('\n\n') : ''
            MessagePlugin.warning({
                content: t('knowledgeBase.uploadPartialSuccess', {
                    success: successCount,
                    fail: failCount
                }) + extraContent,
                duration: 8000,
                closeBtn: true
            })
        } else {
            const { duplicateCount, nonDuplicateText } = summarizeFailedFiles(failedFiles)
            const extraSections: string[] = []
            if (duplicateCount > 0) {
                extraSections.push(t('knowledgeBase.duplicateFilesSkipped', { count: duplicateCount }))
            }
            if (nonDuplicateText) {
                extraSections.push(t('knowledgeBase.failedFilesList') + '\n' + nonDuplicateText)
            }
            const extraContent = extraSections.length ? '\n\n' + extraSections.join('\n\n') : ''
            MessagePlugin.error({
                content: t('knowledgeBase.uploadAllFailed') + extraContent,
                duration: 8000,
                closeBtn: true
            })
        }
    }

    input.value = ''
}

const handleDocFolderUploadClick = async () => {
    const kbId = await ensureDocKnowledgeBaseReady()
    if (!kbId) return
    pendingUploadKbId.value = kbId
    docFolderInput.value?.click()
}

const handleDocFolderChange = async (event: Event) => {
    const input = event.target as HTMLInputElement
    const files = input?.files
    if (!files || files.length === 0) {
        pendingUploadKbId.value = null
        return
    }

    const kbId = pendingUploadKbId.value || (await ensureDocKnowledgeBaseReady())
    pendingUploadKbId.value = null
    if (!kbId) {
        input.value = ''
        return
    }

    // 检查是否启用了VLM
    const vlmEnabled = currentKbInfo.value?.vlm_config?.enabled || false

    // 过滤有效文件（文件夹上传始终使用静默模式）
    const validFiles: File[] = []
    let invalidCount = 0
    let hiddenFileCount = 0
    let imageFilteredCount = 0

    for (let i = 0; i < files.length; i++) {
        const file = files[i]
        const relativePath = (file as any).webkitRelativePath || file.name
        
        // 1. 过滤隐藏文件和隐藏文件夹
        // 检查路径中是否包含以 . 开头的文件或文件夹
        const pathParts = relativePath.split('/')
        const hasHiddenComponent = pathParts.some((part: string) => part.startsWith('.'))
        if (hasHiddenComponent) {
            hiddenFileCount++
            continue
        }
        
        // 2. 如果未启用VLM，过滤图片文件
        if (!vlmEnabled) {
            const fileExt = file.name.substring(file.name.lastIndexOf('.') + 1).toLowerCase()
            const imageTypes = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp']
            if (imageTypes.includes(fileExt)) {
                imageFilteredCount++
                continue
            }
        }
        
        // 3. 文件类型验证（文件夹上传时始终静默过滤）
        if (kbFileTypeVerification(file, true)) {
            invalidCount++
        } else {
            validFiles.push(file)
        }
    }

    // 如果没有有效文件，直接返回
    if (validFiles.length === 0) {
        const totalFiltered = invalidCount + hiddenFileCount + imageFilteredCount
        if (totalFiltered > 0) {
            let filterReasons = []
            if (hiddenFileCount > 0) {
                filterReasons.push(t('knowledgeBase.hiddenFilesFiltered', { count: hiddenFileCount }))
            }
            if (imageFilteredCount > 0) {
                filterReasons.push(t('knowledgeBase.imagesFilteredNoVLM', { count: imageFilteredCount }))
            }
            if (invalidCount > 0) {
                filterReasons.push(t('knowledgeBase.invalidFilesFiltered', { count: invalidCount }))
            }
            MessagePlugin.warning(t('knowledgeBase.noValidFilesInFolder', { total: files.length }) + '\n' + filterReasons.join('\n'))
        } else {
            MessagePlugin.error(t('knowledgeBase.noValidFiles'))
        }
        input.value = ''
        return
    }

    // 显示过滤后的上传提示
    const totalCount = validFiles.length
    const totalFiltered = invalidCount + hiddenFileCount + imageFilteredCount
    if (totalFiltered > 0) {
        let filterInfo = []
        if (hiddenFileCount > 0) {
            filterInfo.push(t('knowledgeBase.hiddenFilesFiltered', { count: hiddenFileCount }))
        }
        if (imageFilteredCount > 0) {
            filterInfo.push(t('knowledgeBase.imagesFilteredNoVLM', { count: imageFilteredCount }))
        }
        if (invalidCount > 0) {
            filterInfo.push(t('knowledgeBase.invalidFilesFiltered', { count: invalidCount }))
        }
        MessagePlugin.info(
            t('knowledgeBase.uploadingValidFiles', {
                valid: totalCount,
                total: files.length
            }) + '\n' + filterInfo.join(', ')
        )
    } else {
        MessagePlugin.info(t('knowledgeBase.uploadingFolder', { total: totalCount }))
    }

    // 批量上传文件夹内容
    let successCount = 0
    let failCount = 0
    const failedFiles: Array<{ name: string; reason: string }> = []

    for (const file of validFiles) {
        // 获取文件的相对路径(webkitRelativePath)，用于保留子目录结构
        const relativePath = (file as any).webkitRelativePath
        let fileName = file.name
        if (relativePath) {
            const pathParts = relativePath.split('/')
            if (pathParts.length > 2) {
                const subPath = pathParts.slice(1, -1).join('/')
                fileName = `${subPath}/${file.name}`
            }
        }

        const uploadId = `${file.name}_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
        let progress = 0
        let status: 'uploading' | 'success' | 'error' = 'uploading'
        let errorReason: string | undefined

        window.dispatchEvent(new CustomEvent('knowledgeFileUploadStart', {
            detail: {
                kbId,
                uploadId,
                fileName
            }
        }))

        try {
            // 获取当前选中的分类ID
            const tagIdToUpload = uiStore.selectedTagId !== '__untagged__' ? uiStore.selectedTagId : undefined
            await uploadKnowledgeFile(
                kbId,
                { file, fileName, tag_id: tagIdToUpload },
                (progressEvent: any) => {
                    if (progressEvent?.total) {
                        progress = Math.round((progressEvent.loaded * 100) / progressEvent.total)
                        window.dispatchEvent(new CustomEvent('knowledgeFileUploadProgress', {
                            detail: {
                                kbId,
                                uploadId,
                                progress
                            }
                        }))
                    }
                }
            )
            successCount++
            status = 'success'
            progress = 100
        } catch (error: any) {
            failCount++
            errorReason = error?.error?.message || error?.message || t('knowledgeBase.uploadFailed')
            if (error?.code === 'duplicate_file' || error?.error?.code === 'duplicate_file') {
                errorReason = t('knowledgeBase.fileExists')
            }
            failedFiles.push({ name: fileName, reason: errorReason })
            status = 'error'
        } finally {
            window.dispatchEvent(new CustomEvent('knowledgeFileUploadComplete', {
                detail: {
                    kbId,
                    uploadId,
                    status,
                    progress,
                    error: errorReason,
                    fileName
                }
            }))
        }
    }

    if (successCount > 0) {
        window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
            detail: { kbId }
        }))
    }

    if (failCount === 0) {
        MessagePlugin.success(t('knowledgeBase.uploadAllSuccess', { count: successCount }))
    } else if (successCount > 0) {
        const { duplicateCount, nonDuplicateText } = summarizeFailedFiles(failedFiles)
        const extraSections: string[] = []
        if (duplicateCount > 0) {
            extraSections.push(t('knowledgeBase.duplicateFilesSkipped', { count: duplicateCount }))
        }
        if (nonDuplicateText) {
            extraSections.push(t('knowledgeBase.failedFilesList') + '\n' + nonDuplicateText)
        }
        const extraContent = extraSections.length ? '\n\n' + extraSections.join('\n\n') : ''
        MessagePlugin.warning({
            content: t('knowledgeBase.uploadPartialSuccess', {
                success: successCount,
                fail: failCount
            }) + extraContent,
            duration: 8000,
            closeBtn: true
        })
    } else {
        const { duplicateCount, nonDuplicateText } = summarizeFailedFiles(failedFiles)
        const extraSections: string[] = []
        if (duplicateCount > 0) {
            extraSections.push(t('knowledgeBase.duplicateFilesSkipped', { count: duplicateCount }))
        }
        if (nonDuplicateText) {
            extraSections.push(t('knowledgeBase.failedFilesList') + '\n' + nonDuplicateText)
        }
        const extraContent = extraSections.length ? '\n\n' + extraSections.join('\n\n') : ''
        MessagePlugin.error({
            content: t('knowledgeBase.uploadAllFailed') + extraContent,
            duration: 8000,
            closeBtn: true
        })
    }

    input.value = ''
}

const handleDocManualCreate = async () => {
    const kbId = await ensureDocKnowledgeBaseReady()
    if (!kbId) return
    uiStore.openManualEditor({
        mode: 'create',
        kbId,
        status: 'draft',
        onSuccess: ({ kbId: savedKbId }) => {
            if (savedKbId) {
                window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', { detail: { kbId: savedKbId } }))
            }
        },
    })
}

const handleDocURLImport = async () => {
    const kbId = await ensureDocKnowledgeBaseReady()
    if (!kbId) return
    
    window.dispatchEvent(new CustomEvent('openURLImportDialog', {
        detail: { kbId }
    }))
}

const dispatchFaqMenuAction = (action: 'create' | 'import' | 'search' | 'export' | 'batch' | 'batchTag' | 'batchEnable' | 'batchDisable' | 'batchDelete', kbId: string) => {
    window.dispatchEvent(new CustomEvent('faqMenuAction', {
        detail: { action, kbId }
    }))
}

const handleFaqCreateFromMenu = async () => {
    const kbId = await getCurrentKbId()
    if (!kbId) {
        MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
        return
    }
    dispatchFaqMenuAction('create', kbId)
}

const handleFaqImportFromMenu = async () => {
    const kbId = await getCurrentKbId()
    if (!kbId) {
        MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
        return
    }
    dispatchFaqMenuAction('import', kbId)
}

const handleFaqSearchTestFromMenu = async () => {
    const kbId = await getCurrentKbId()
    if (!kbId) {
        MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
        return
    }
    dispatchFaqMenuAction('search', kbId)
}

const handleFaqExportFromMenu = async () => {
    const kbId = await getCurrentKbId()
    if (!kbId) {
        MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
        return
    }
    dispatchFaqMenuAction('export', kbId)
}

const faqBatchActionOptions = computed(() => {
  if (selectedFaqCount.value === 0) {
    return []
  }
  const options = [
    { 
      content: `${t('knowledgeEditor.faq.batchUpdateTag')} (${selectedFaqCount.value})`, 
      value: 'batchTag', 
      icon: 'folder'
    }
  ]
  
  // 根据选中条目的状态显示批量启用或禁用
  if (selectedFaqDisabledCount.value > 0) {
    options.push({
      content: `${t('knowledgeEditor.faq.batchEnable')} (${selectedFaqDisabledCount.value})`,
      value: 'batchEnable',
      icon: 'check-circle',
    })
  }
  if (selectedFaqEnabledCount.value > 0) {
    options.push({
      content: `${t('knowledgeEditor.faq.batchDisable')} (${selectedFaqEnabledCount.value})`,
      value: 'batchDisable',
      icon: 'close-circle',
    })
  }
  
  options.push({
    content: `${t('knowledgeEditor.faqImport.deleteSelected')} (${selectedFaqCount.value})`,
    value: 'batchDelete',
    icon: 'delete',
  })
  
  return options
})

const handleFaqBatchActionFromMenu = async (data: { value: string }) => {
  const kbId = await getCurrentKbId()
  if (!kbId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'))
    return
  }
  if (selectedFaqCount.value === 0) {
    MessagePlugin.warning(t('knowledgeEditor.faq.selectEntriesFirst') || '请先选中要操作的FAQ条目')
    return
  }
  dispatchFaqMenuAction(data.value as 'batchTag' | 'batchEnable' | 'batchDisable' | 'batchDelete', kbId)
}

const handleCreateKnowledgeBase = () => {
    uiStore.openCreateKB()
}

const handleCreateAgent = () => {
    // 触发创建智能体事件，由 AgentList 页面监听处理
    window.dispatchEvent(new CustomEvent('openAgentEditor', {
        detail: { mode: 'create' }
    }))
}

</script>
<style lang="less" scoped>
.aside_box {
    min-width: 260px;
    padding: 8px;
    background: #fff;
    box-sizing: border-box;
    height: 100vh;
    overflow: hidden;
    display: flex;
    flex-direction: column;

    .logo_box {
        height: 80px;
        display: flex;
        align-items: center;
        .logo{
            width: 134px;
            height: auto;
            margin-left: 24px;
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
        color: #000000;
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

    .kb-action-wrapper {
        border: 1px solid #e7e9eb;
        border-radius: 8px;
        padding: 8px;
        margin-bottom: 12px;
        background: #fafcfc;
    }

    .kb-action-label {
        font-size: 11px;
        font-weight: 600;
        color: #8b9196;
        margin-bottom: 6px;
        padding: 0 4px;
        text-transform: uppercase;
        letter-spacing: 0.3px;
    }

    .kb-action-menu {
        display: flex;
        flex-direction: column;
        gap: 3px;
    }

    .kb-action-item {
        background: #fff;
        border-radius: 6px;
        border: 1px solid #eef1f2;
        transition: background-color 0.08s ease, border-color 0.08s ease;
        display: flex;
        align-items: center;
        gap: 8px;
        padding: 8px 10px;
        cursor: pointer;

        &:hover {
            background: #f0fdf6;
            border-color: #10b981;

            .kb-action-icon {
                color: #059669;
            }

            .kb-action-title {
                color: #10b981;
            }
        }

        &:active {
            background: #e6f9f0;
        }
    }

    .kb-action-icon-wrapper {
        width: 28px;
        height: 28px;
        border-radius: 5px;
        background: #f0fdf6;
        display: flex;
        align-items: center;
        justify-content: center;
        flex-shrink: 0;
        transition: background-color 0.08s ease;
    }

    .kb-action-icon {
        color: #10b981;
        transition: color 0.08s ease;
    }

    .kb-action-item:hover .kb-action-icon-wrapper {
        background: #d1fae5;
    }

    .kb-action-content {
        flex: 1;
        min-width: 0;
        display: flex;
        align-items: center;
        white-space: nowrap;
    }

    .kb-action-title {
        font-size: 13px;
        font-weight: 500;
        color: #0f172a;
        transition: color 0.08s ease;
        display: inline;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        flex-shrink: 1;
        min-width: 0;
    }

    .kb-action-count {
        font-size: 12px;
        color: #10b981;
        font-weight: 600;
        margin-left: 4px;
        flex-shrink: 0;
        white-space: nowrap;
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
        background-color: #dbede4;
        color: #07C05F;

    }

    .upload-file-icon {
        width: 20px;
        height: 20px;
        color: rgba(0, 0, 0, 0.6);
    }

    .active-upload {
        color: #07C05F;
    }

    .menu_item_active {
        border-radius: 4px;
        background: #07c05f1a !important;

        .menu_icon,
        .menu_title {
            color: #07c05f !important;
        }

        .menu-create-hint {
            color: #07c05f !important;
            opacity: 1;
        }
    }

    .menu_item_c_active {

        .menu_icon,
        .menu_title {
            color: #000000e6;
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

        .menu_item-box {
            display: flex;
            align-items: center;
        }

        &:hover {
            border-radius: 4px;
            background: #30323605;
            color: #00000099;

            .menu_icon,
            .menu_title {
                color: #00000099;
            }
        }
    }

    .menu_icon {
        display: flex;
        margin-right: 10px;
        color: #00000099;

        .icon {
            width: 20px;
            height: 20px;
            fill: currentColor;
            overflow: hidden;
        }
    }

    .menu_title {
        color: #00000099;
        text-overflow: ellipsis;
        font-family: "PingFang SC";
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
        font-family: "PingFang SC";
        font-size: 14px;
        font-style: normal;
        overflow-y: auto;
        scrollbar-width: none;
        flex: 1;
        min-height: 0;
        margin-left: 4px;
    }
    
    .timeline_header {
        font-family: "PingFang SC";
        font-size: 12px;
        font-weight: 600;
        color: #00000066;
        padding: 12px 18px 6px 18px;
        margin-top: 8px;
        line-height: 20px;
        user-select: none;
        
        &:first-child {
            margin-top: 4px;
        }
    }

    .submenu_item_p {
        height: 44px;
        padding: 4px 0px 4px 0px;
        box-sizing: border-box;
    }


    .submenu_item {
        cursor: pointer;
        display: flex;
        align-items: center;
        color: #00000099;
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
            color: #07C05F;
        }

        .sub_title {
            margin-left: 14px;
        }

        &:hover {
            background: #30323605;
            color: #00000099;
            border-radius: 3px;

            .menu-more {
                color: #00000099;
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
        background: #07c05f1a !important;
        color: #07c05f !important;
        border-radius: 3px;

        .menu-more {
            color: #07c05f !important;
        }

        .menu-more-wrap {
            opacity: 1;
        }

        .submenu_title {
            max-width: 160px !important;
        }
    }
}

/* 知识库下拉菜单样式 */
.kb-dropdown-icon {
    margin-left: auto;
    color: #666;
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
        color: #07c05f;
    }
    
    &.active {
        color: #07c05f;
    }
    
    &.active:hover {
        color: #05a04f;
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
    background: #fff;
    border: 1px solid #e5e7eb;
    border-radius: 6px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
    z-index: 1000;
    max-height: 200px;
    overflow-y: auto;
}

.kb-dropdown-item {
    padding: 8px 16px;
    cursor: pointer;
    transition: background-color 0.2s ease;
    font-size: 14px;
    color: #333;
    
    &:hover {
        background-color: #f5f5f5;
    }
    
    &.active {
        background-color: #07c05f1a;
        color: #07c05f;
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
    color: #07c05f;
    opacity: 0.7;
    transition: opacity 0.2s ease;
    flex-shrink: 0;
}

.menu_item:hover .menu-create-hint {
    opacity: 1;
}

.menu_box {
    position: relative;
}

.kb-upload-input {
    display: none;
}
</style>
<style lang="less">
// 上传操作下拉菜单样式 - 全局样式（因为 TDesign 的下拉菜单挂载到 body 上）
// 使用更具体的选择器来匹配上传操作下拉菜单
.t-popup[data-popper-placement^="right"] {
    .t-popup__content {
        .t-dropdown__menu {
            background: #ffffff !important;
            border: 1px solid #e5e7eb !important;
            border-radius: 6px !important;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1) !important;
            padding: 4px !important;
            min-width: 100px !important;
        }

        .t-dropdown__item {
            padding: 8px 12px !important;
            border-radius: 4px !important;
            margin: 2px 0 !important;
            transition: all 0.2s ease !important;
            font-size: 14px !important;
            color: #333333 !important;
            min-width: auto !important;
            max-width: none !important;
            width: auto !important;
            cursor: pointer !important;

            &:hover {
                background: #f5f7fa !important;
                color: #07c05f !important;
            }

            .t-dropdown__item-text {
                color: inherit !important;
                font-size: 14px !important;
                line-height: 20px !important;
                white-space: nowrap !important;
            }
        }
    }
}

// 退出登录确认框样式
:deep(.t-popconfirm) {
    .t-popconfirm__content {
        background: #fff;
        border: 1px solid #e7e7e7;
        border-radius: 6px;
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
        padding: 12px 16px;
        font-size: 14px;
        color: #333;
        max-width: 200px;
    }
    
    .t-popconfirm__arrow {
        border-bottom-color: #e7e7e7;
    }
    
    .t-popconfirm__arrow::after {
        border-bottom-color: #fff;
    }
    
    .t-popconfirm__buttons {
        margin-top: 8px;
        display: flex;
        justify-content: flex-end;
        gap: 8px;
    }
    
    .t-button--variant-outline {
        border-color: #d9d9d9;
        color: #666;
    }
    
    .t-button--theme-danger {
        background-color: #ff4d4f;
        border-color: #ff4d4f;
    }
    
    .t-button--theme-danger:hover {
        background-color: #ff7875;
        border-color: #ff7875;
    }
}
</style>