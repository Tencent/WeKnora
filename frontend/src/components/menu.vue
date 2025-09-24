<template>
    <div class="aside_box">
        <div class="logo_box" @click="router.push('/platform/knowledge-bases')" style="cursor: pointer;">
            <img class="logo" src="@/assets/img/weknora.png" alt="">
        </div>
        
        <!-- 上半部分：知识库和对话 -->
        <div class="menu_top">
            <div class="menu_box" :class="{ 'has-submenu': item.children }" v-for="(item, index) in topMenuItems" :key="index">
                <div @click="handleMenuClick(item.path)"
                    @mouseenter="mouseenteMenu(item.path)" @mouseleave="mouseleaveMenu(item.path)"
                     :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : isMenuItemActive(item.path) ? 'menu_item_active' : '']">
                    <div class="menu_item-box">
                        <div class="menu_icon">
                            <img class="icon" :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon :  item.icon == 'logout' ? logoutIcon : item.icon == 'tenant' ? tenantIcon : prefixIcon)" alt="">
                        </div>
                        <span class="menu_title" :title="item.path === 'knowledge-bases' && kbMenuItem ? kbMenuItem.title : item.title">{{ item.path === 'knowledge-bases' && kbMenuItem ? kbMenuItem.title : item.title }}</span>
                        <!-- 知识库切换下拉箭头 -->
                        <div v-if="item.path === 'knowledge-bases' && isInKnowledgeBase" 
                             class="kb-dropdown-icon" 
                             :class="{ 
                                 'rotate-180': showKbDropdown,
                                 'active': isMenuItemActive(item.path)
                             }"
                             @click.stop="toggleKbDropdown">
                            <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor">
                                <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z"/>
                            </svg>
                        </div>
                    </div>
                    <!-- 知识库切换下拉菜单 -->
                    <div v-if="item.path === 'knowledge-bases' && showKbDropdown && isInKnowledgeBase" 
                         class="kb-dropdown-menu">
                        <div v-for="kb in initializedKnowledgeBases" 
                             :key="kb.id" 
                             class="kb-dropdown-item"
                             :class="{ 'active': kb.name === currentKbName }"
                             @click.stop="switchKnowledgeBase(kb.id)">
                            {{ kb.name }}
                        </div>
                    </div>
                    <t-popup overlayInnerClassName="upload-popup" class="placement top center" content="上传知识"
                        placement="top" show-arrow destroy-on-close>
                        <div class="upload-file-wrap" @click.stop="uploadFile" variant="outline"
                             v-if="item.path === 'knowledge-bases' && $route.name === 'knowledgeBaseDetail'">
                            <img class="upload-file-icon" :class="[item.path == currentpath ? 'active-upload' : '']"
                                :src="getImgSrc(fileAddIcon)" alt="">
                        </div>
                    </t-popup>
                </div>
                <div ref="submenuscrollContainer" @scroll="handleScroll" class="submenu" v-if="item.children">
                    <div class="submenu_item_p" v-for="(subitem, subindex) in item.children" :key="subindex"
                        @click="gotopage(subitem.path)">
                        <div :class="['submenu_item', currentSecondpath == subitem.path ? 'submenu_item_active' : '']"
                            @mouseenter="mouseenteBotDownr(subindex)" @mouseleave="mouseleaveBotDown">
                            <i v-if="currentSecondpath == subitem.path" class="dot"></i>
                            <span class="submenu_title"
                                :style="currentSecondpath == subitem.path ? 'margin-left:14px;max-width:160px;' : 'margin-left:18px;max-width:173px;'">
                                {{ subitem.title }}
                            </span>
                            <t-popup v-model:visible="subitem.isMore" @overlay-click="delCard(subindex, subitem)"
                                @visible-change="onVisibleChange" overlayClassName="del-menu-popup" trigger="click"
                                destroy-on-close placement="top-left">
                                <div v-if="(activeSubmenu == subindex) || (currentSecondpath == subitem.path) || subitem.isMore"
                                    @click.stop="openMore(subindex)" variant="outline" class="menu-more-wrap">
                                    <t-icon name="ellipsis" class="menu-more" />
                                </div>
                                <template #content>
                                    <span class="del_submenu">删除记录</span>
                                </template>
                            </t-popup>
                        </div>
                    </div>
                </div>
            </div>
        </div>
        
        <!-- 下半部分：账户信息、系统设置、退出登录 -->
        <div class="menu_bottom">
            <div class="menu_box" v-for="(item, index) in bottomMenuItems" :key="'bottom-' + index">
                <div v-if="item.path === 'logout'">
                    <t-popconfirm 
                        content="确定要退出登录吗？" 
                        @confirm="handleLogout"
                        placement="top"
                        :show-arrow="true"
                    >
                        <div @mouseenter="mouseenteMenu(item.path)" @mouseleave="mouseleaveMenu(item.path)"
                            :class="['menu_item', 'logout-item']">
                            <div class="menu_item-box">
                                <div class="menu_icon">
                                    <img class="icon" :src="getImgSrc(logoutIcon)" alt="">
                                </div>
                                <span class="menu_title">{{ item.title }}</span>
                            </div>
                        </div>
                    </t-popconfirm>
                </div>
                <div v-else @click="handleMenuClick(item.path)"
                    @mouseenter="mouseenteMenu(item.path)" @mouseleave="mouseleaveMenu(item.path)"
                    :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : (item.path == currentpath) ? 'menu_item_active' : '']">
                    <div class="menu_item-box">
                        <div class="menu_icon">
                            <img class="icon" :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon : item.icon == 'tenant' ? tenantIcon : prefixIcon)" alt="">
                        </div>
                        <span class="menu_title">{{ item.path === 'knowledge-bases' && kbMenuItem ? kbMenuItem.title : item.title }}</span>
                    </div>
                </div>
            </div>
        </div>
        
        <input type="file" @change="upload" style="display: none" ref="uploadInput"
            accept=".pdf,.docx,.doc,.txt,.md,.jpg,.jpeg,.png" />
    </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia';
import { onMounted, watch, computed, ref, reactive, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getSessionsList, delSession } from "@/api/chat/index";
import { getKnowledgeBaseById, listKnowledgeBases, uploadKnowledgeFile } from '@/api/knowledge-base';
import { kbFileTypeVerification } from '@/utils/index';
import { useMenuStore } from '@/stores/menu';
import { useAuthStore } from '@/stores/auth';
import { MessagePlugin } from "tdesign-vue-next";
let uploadInput = ref();
const usemenuStore = useMenuStore();
const authStore = useAuthStore();
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
let activeSubmenu = ref<number>(-1);

// 是否处于知识库详情页
const isInKnowledgeBase = computed<boolean>(() => {
    return route.name === 'knowledgeBaseDetail' || 
           route.name === 'kbCreatChat' || 
           route.name === 'chat' || 
           route.name === 'knowledgeBaseSettings';
});

// 统一的菜单项激活状态判断
const isMenuItemActive = (itemPath: string): boolean => {
    const currentRoute = route.name;
    
    switch (itemPath) {
        case 'knowledge-bases':
            return currentRoute === 'knowledgeBaseList' || 
                   currentRoute === 'knowledgeBaseDetail' || 
                   currentRoute === 'knowledgeBaseSettings';
        case 'creatChat':
            return currentRoute === 'kbCreatChat';
        case 'tenant':
            return currentRoute === 'tenant';
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
        isCreatChatActive: itemPath === 'creatChat' && currentRoute === 'kbCreatChat',
        isTenantActive: itemPath === 'tenant' && currentRoute === 'tenant',
        isChatActive: itemPath === 'chat' && currentRoute === 'chat'
    };
};

// 分离上下两部分菜单
const topMenuItems = computed<MenuItem[]>(() => {
    return (menuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => 
        item.path === 'knowledge-bases' || (isInKnowledgeBase.value && item.path === 'creatChat')
    );
});

const bottomMenuItems = computed<MenuItem[]>(() => {
    return (menuArr.value as unknown as MenuItem[]).filter((item: MenuItem) => {
        if (item.path === 'knowledge-bases' || item.path === 'creatChat') {
            return false;
        }
        return true;
    });
});

// 当前知识库名称和列表
const currentKbName = ref<string>('')
const allKnowledgeBases = ref<Array<{ id: string; name: string; embedding_model_id?: string; summary_model_id?: string }>>([])
const showKbDropdown = ref<boolean>(false)

// 过滤已初始化的知识库
const initializedKnowledgeBases = computed(() => {
    return allKnowledgeBases.value.filter(kb => 
        kb.embedding_model_id && kb.embedding_model_id !== '' && 
        kb.summary_model_id && kb.summary_model_id !== ''
    )
})

// 动态更新知识库菜单项标题
const kbMenuItem = computed(() => {
    const kbItem = topMenuItems.value.find(item => item.path === 'knowledge-bases')
    if (kbItem && isInKnowledgeBase.value && currentKbName.value) {
        return { ...kbItem, title: currentKbName.value }
    }
    return kbItem
})

const loading = ref(false)
const uploadFile = async () => {
    // 获取当前知识库ID
    const currentKbId = await getCurrentKbId();
    
    // 检查当前知识库的初始化状态
    if (currentKbId) {
        try {
            const kbResponse = await getKnowledgeBaseById(currentKbId);
            const kb = kbResponse.data;
            
            // 检查知识库是否已初始化（有 EmbeddingModelID 和 SummaryModelID）
            if (!kb.embedding_model_id || kb.embedding_model_id === '' || 
                !kb.summary_model_id || kb.summary_model_id === '') {
                MessagePlugin.warning("该知识库尚未完成初始化配置，请先前往设置页面配置模型信息后再上传文件");
                return;
            }
        } catch (error) {
            console.error('获取知识库信息失败:', error);
            MessagePlugin.error("获取知识库信息失败，无法上传文件");
            return;
        }
    }
    
    uploadInput.value.click()
}
const upload = async (e: any) => {
    const file = e.target.files[0];
    if (!file) return;
    
    // 文件类型验证
    if (kbFileTypeVerification(file)) {
        return;
    }
    
    // 获取当前知识库ID
    const currentKbId = (route.params as any)?.kbId as string;
    if (!currentKbId) {
        MessagePlugin.error("缺少知识库ID");
        return;
    }
    
    try {
        const result = await uploadKnowledgeFile(currentKbId, { file });
        const responseData = result as any;
        console.log('上传API返回结果:', responseData);
        
        // 如果没有抛出异常，就认为上传成功，先触发刷新事件
        console.log('文件上传完成，发送事件通知页面刷新，知识库ID:', currentKbId);
        window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', { 
            detail: { kbId: currentKbId } 
        }));
        
        // 然后处理UI消息
        // 判断上传是否成功 - 检查多种可能的成功标识
        const isSuccess = responseData.success || responseData.code === 200 || responseData.status === 'success' || (!responseData.error && responseData);
        
        if (isSuccess) {
            MessagePlugin.info("上传成功！");
        } else {
            // 改进错误信息提取逻辑
            let errorMessage = "上传失败！";
            if (responseData.error && responseData.error.message) {
                errorMessage = responseData.error.message;
            } else if (responseData.message) {
                errorMessage = responseData.message;
            }
            if (responseData.code === 'duplicate_file' || (responseData.error && responseData.error.code === 'duplicate_file')) {
                errorMessage = "文件已存在";
            }
            MessagePlugin.error(errorMessage);
        }
    } catch (err: any) {
        let errorMessage = "上传失败！";
        if (err.code === 'duplicate_file') {
            errorMessage = "文件已存在";
        } else if (err.error && err.error.message) {
            errorMessage = err.error.message;
        } else if (err.message) {
            errorMessage = err.message;
        }
        MessagePlugin.error(errorMessage);
    } finally {
        uploadInput.value.value = "";
    }
}
const mouseenteBotDownr = (val: number) => {
    activeSubmenu.value = val;
}
const mouseleaveBotDown = () => {
    activeSubmenu.value = -1;
}
const onVisibleChange = (_e: any) => {
}

const delCard = (index: number, item: any) => {
    delSession(item.id).then((res: any) => {
        if (res && (res as any).success) {
            (menuArr.value as any[])[1]?.children?.splice(index, 1);
            if (item.id == route.params.chatid) {
                // 删除当前会话后，跳转到当前知识库的创建聊天页面
                const kbId = route.params.kbId;
                if (kbId) {
                    router.push(`/platform/knowledge-bases/${kbId}/creatChat`);
                } else {
                    router.push('/platform/knowledge-bases');
                }
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
    if (!container) return

    const { scrollTop, scrollHeight, clientHeight } = container[0]
    const isBottom = scrollHeight - (scrollTop + clientHeight) < 100 // 触底阈值
    if (isBottom && hasMore.value) {
        currentPage.value++;
        getMessageList();
    }
}
const handleScroll = debounce(checkScrollBottom, 200)
const getMessageList = async () => {
    // 仅在知识库内部显示对话列表
    if (!isInKnowledgeBase.value) {
        usemenuStore.clearMenuArr();
        currentKbName.value = '';
        return;
    }
    let kbId = (route.params as any)?.kbId as string
    // 新的路由格式：/platform/chat/:kbId/:chatid，直接从路由参数获取知识库ID
    if (!kbId) {
        usemenuStore.clearMenuArr();
        currentKbName.value = '';
        return;
    }
    
    // 获取知识库名称和所有知识库列表
    try {
        const [kbRes, allKbRes]: any[] = await Promise.all([
            getKnowledgeBaseById(kbId),
            listKnowledgeBases()
        ])
        if (kbRes?.data?.name) {
            currentKbName.value = kbRes.data.name
        }
        if (allKbRes?.data) {
            allKnowledgeBases.value = allKbRes.data
        }
    } catch {}
    
    if (loading.value) return;
    loading.value = true;
    usemenuStore.clearMenuArr();
    getSessionsList(currentPage.value, page_size.value).then((res: any) => {
        if (res.data && res.data.length) {
            // 过滤出当前知识库的会话
            const filtered = res.data.filter((s: any) => s.knowledge_base_id === kbId)
            filtered.forEach((item: any) => {
                let obj = { title: item.title ? item.title : "新会话", path: `chat/${kbId}/${item.id}`, id: item.id, isMore: false, isNoTitle: item.title ? false : true }
                usemenuStore.updatemenuArr(obj)
            });
            loading.value = false;
        }
        if ((res as any).total) {
            total.value = (res as any).total;
        }
    })
}

const openMore = (_e: any) => { }
onMounted(() => {
    const routeName = typeof route.name === 'string' ? route.name : (route.name ? String(route.name) : '')
    currentpath.value = routeName;
    if (route.params.chatid && route.params.kbId) {
        currentSecondpath.value = `chat/${route.params.kbId}/${route.params.chatid}`;
    }
    getMessageList();
});

watch([() => route.name, () => route.params], (newvalue) => {
    const nameStr = typeof newvalue[0] === 'string' ? (newvalue[0] as string) : (newvalue[0] ? String(newvalue[0]) : '')
    currentpath.value = nameStr;
    if (newvalue[1].chatid && newvalue[1].kbId) {
        currentSecondpath.value = `chat/${newvalue[1].kbId}/${newvalue[1].chatid}`;
    } else {
        currentSecondpath.value = "";
    }
    // 路由变化时刷新对话列表（仅在知识库内部）
    getMessageList();
    // 路由变化时更新图标状态
    getIcon(nameStr);
});
let fileAddIcon = ref('file-add-green.svg');
let knowledgeIcon = ref('zhishiku-green.svg');
let prefixIcon = ref('prefixIcon.svg');
let logoutIcon = ref('logout.svg');
let tenantIcon = ref('user.svg'); // 使用专门的用户图标
let pathPrefix = ref(route.name)
  const getIcon = (path: string) => {
      // 根据当前路由状态更新所有图标
      const kbActiveState = getIconActiveState('knowledge-bases');
      const creatChatActiveState = getIconActiveState('creatChat');
      const tenantActiveState = getIconActiveState('tenant');
      
      // 上传图标：只在知识库相关页面显示绿色
      fileAddIcon.value = kbActiveState.isKbActive ? 'file-add-green.svg' : 'file-add.svg';
      
      // 知识库图标：只在知识库页面显示绿色
      knowledgeIcon.value = kbActiveState.isKbActive ? 'zhishiku-green.svg' : 'zhishiku.svg';
      
      // 对话图标：只在对话创建页面显示绿色，在知识库页面显示灰色，其他情况显示默认
      prefixIcon.value = creatChatActiveState.isCreatChatActive ? 'prefixIcon-green.svg' : 
                        kbActiveState.isKbActive ? 'prefixIcon-grey.svg' : 
                        'prefixIcon.svg';
      
      // 租户图标：只在租户页面显示绿色
      tenantIcon.value = tenantActiveState.isTenantActive ? 'user-green.svg' : 'user.svg';
      
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
    } else {
        gotopage(path)
    }
}

// 处理退出登录确认
const handleLogout = () => {
    gotopage('logout')
}

const getCurrentKbId = async (): Promise<string | null> => {
    let kbId = (route.params as any)?.kbId as string
    // 新的路由格式：/platform/chat/:kbId/:chatid，直接从路由参数获取
    if (!kbId && route.name === 'chat' && (route.params as any)?.kbId) {
        kbId = (route.params as any).kbId
    }
    return kbId || null
}

const gotopage = async (path: string) => {
    pathPrefix.value = path;
    // 处理退出登录
    if (path === 'logout') {
        authStore.logout();
        router.push('/login');
        return;
    } else {
        if (path === 'creatChat') {
            const kbId = await getCurrentKbId()
            if (kbId) {
                router.push(`/platform/knowledge-bases/${kbId}/creatChat`)
            } else {
                router.push(`/platform/knowledge-bases`)
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

// 知识库下拉相关方法
const toggleKbDropdown = (event?: Event) => {
    if (event) {
        event.stopPropagation()
    }
    showKbDropdown.value = !showKbDropdown.value
}

const switchKnowledgeBase = (kbId: string, event?: Event) => {
    if (event) {
        event.stopPropagation()
    }
    showKbDropdown.value = false
    const currentRoute = route.name
    
    // 路由跳转
    if (currentRoute === 'knowledgeBaseDetail') {
        router.push(`/platform/knowledge-bases/${kbId}`)
    } else if (currentRoute === 'kbCreatChat') {
        router.push(`/platform/knowledge-bases/${kbId}/creatChat`)
    } else if (currentRoute === 'knowledgeBaseSettings') {
        router.push(`/platform/knowledge-bases/${kbId}/settings`)
    } else {
        router.push(`/platform/knowledge-bases/${kbId}`)
    }
    
    // 刷新右侧内容 - 通过触发页面重新加载或发送事件
    nextTick(() => {
        // 发送全局事件通知页面刷新知识库内容
        window.dispatchEvent(new CustomEvent('knowledgeBaseChanged', { 
            detail: { kbId } 
        }))
    })
}

// 点击外部关闭下拉菜单
const handleClickOutside = () => {
    showKbDropdown.value = false
}

onMounted(() => {
    document.addEventListener('click', handleClickOutside)
})

watch(() => route.params.kbId, () => {
    showKbDropdown.value = false
})

</script>
<style lang="less" scoped>
.del_submenu {
    color: #fa5151;
    cursor: pointer;
}

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

    .submenu_item_p {
        height: 44px;
        padding: 4px 8px 4px 12px;
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
        padding-left: 18px;
        padding-right: 14px;
        position: relative;

        .submenu_title {
            overflow: hidden;
            white-space: nowrap;
            text-overflow: ellipsis;
        }

        .menu-more-wrap {
            margin-left: auto;
        }

        .menu-more {
            display: inline-block;
            font-weight: bold;
            color: #07C05F;
        }

        .dot {
            width: 4px;
            height: 4px;
            border-radius: 50%;
            background: #07C05F;
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

.menu_box {
    position: relative;
}
</style>
<style lang="less">
.upload-popup {
    background-color: rgba(0, 0, 0, 0.9);
    color: #FFFFFF;
    border-color: rgba(0, 0, 0, 0.9) !important;
    box-shadow: none;
    margin-bottom: 10px !important;

    .t-popup__arrow::before {
        border-color: rgba(0, 0, 0, 0.9) !important;
        background-color: rgba(0, 0, 0, 0.9) !important;
        box-shadow: none !important;
    }
}

.del-menu-popup {
    z-index: 99 !important;

    .t-popup__content {
        width: 100px;
        height: 40px;
        line-height: 30px;
        padding-left: 14px;
        cursor: pointer;
        margin-top: 4px !important;

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