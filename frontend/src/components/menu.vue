<template>
    <div class="aside_box">
        <div class="logo_box">
            <img class="logo" src="@/assets/img/weknora.png" alt="">
        </div>
        <div class="menu_box" v-for="(item, index) in menuArr" :key="index">
            <div @click="gotopage(item.path)"
                @mouseenter="mouseenteMenu(item.path)" @mouseleave="mouseleaveMenu(item.path)"
                :class="['menu_item', item.childrenPath && item.childrenPath == currentpath ? 'menu_item_c_active' : item.path == currentpath ? 'menu_item_active' : '']">
                <div class="menu_item-box">
                    <div class="menu_icon">
                        <img class="icon" :src="getImgSrc(item.icon == 'zhishiku' ? knowledgeIcon : item.icon == 'setting' ? settingIcon : prefixIcon)" alt="">
                    </div>
                    <span class="menu_title">{{ item.title }}</span>
                </div>
                <t-popup overlayInnerClassName="upload-popup" class="placement top center" content="上传知识"
                    placement="top" show-arrow destroy-on-close>
                    <div class="upload-file-wrap" @click="uploadFile" variant="outline"
                        v-if="item.path == 'knowledgeBase'">
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
        <input type="file" @change="upload" style="display: none" ref="uploadInput"
            accept=".pdf,.docx,.doc,.txt,.md,.jpg,.jpeg,.png" />
    </div>
</template>

<script setup>
import { storeToRefs } from 'pinia';
import { onMounted, watch, computed, ref, reactive } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { getSessionsList, delSession } from "@/api/chat/index";
import { useMenuStore } from '@/stores/menu';
import useKnowledgeBase from '@/hooks/useKnowledgeBase';
import { MessagePlugin } from "tdesign-vue-next";
let { requestMethod } = useKnowledgeBase()
let uploadInput = ref();
const usemenuStore = useMenuStore();
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
const { menuArr } = storeToRefs(usemenuStore);
let activeSubmenu = ref(-1);
const loading = ref(false)
const uploadFile = () => {
    uploadInput.value.click()
}
const upload = (e) => {
    requestMethod(e.target.files[0], uploadInput)
}
const mouseenteBotDownr = (val) => {
    activeSubmenu.value = val;
}
const mouseleaveBotDown = () => {
    activeSubmenu.value = -1;
}
const onVisibleChange = (e) => {
}

const delCard = (index, item) => {
    delSession(item.id).then(res => {
        if (res && res.success) {
            menuArr.value[1].children.splice(index, 1);
            if (item.id == route.params.chatid) {
                router.push('/platform/creatChat');
            }
        } else {
            MessagePlugin.error("删除失败，请稍后再试!");
        }
    })
}
const debounce = (fn, delay) => {
    let timer
    return (...args) => {
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
const getMessageList = () => {
    if (loading.value) return;
    loading.value = true;
    usemenuStore.clearMenuArr();
    getSessionsList(currentPage.value, page_size.value).then(res => {
        if (res.data && res.data.length) {
            res.data.forEach(item => {
                let obj = { title: item.title ? item.title : "新会话", path: `chat/${item.id}`, id: item.id, isMore: false, isNoTitle: item.title ? false : true }
                usemenuStore.updatemenuArr(obj)
            });
            loading.value = false;
        }
        if (res.total) {
            total.value = res.total;
        }
    })
}

const openMore = (e) => { }
onMounted(() => {
    currentpath.value = route.name;
    if (route.params.chatid) {
        currentSecondpath.value = `${route.name}/${route.params.chatid}`;
    }
    getMessageList();
});

watch([() => route.name, () => route.params], (newvalue) => {
    currentpath.value = newvalue[0];
    if (newvalue[1].chatid) {
        currentSecondpath.value = `${newvalue[0]}/${newvalue[1].chatid}`;
    } else {
        currentSecondpath.value = "";
    }

});
let fileAddIcon = ref('file-add-green.svg');
let knowledgeIcon = ref('zhishiku-green.svg');
let prefixIcon = ref('prefixIcon.svg');
let settingIcon = ref('setting.svg');
let pathPrefix = ref(route.name)
const getIcon = (path) => {
    fileAddIcon.value = path == 'knowledgeBase' ? 'file-add-green.svg' : 'file-add.svg';
    knowledgeIcon.value = path == 'knowledgeBase' ? 'zhishiku-green.svg' : 'zhishiku.svg';
    prefixIcon.value = path == 'creatChat' ? 'prefixIcon-green.svg' : path == 'knowledgeBase' ? 'prefixIcon-grey.svg' : 'prefixIcon.svg';
    settingIcon.value = path == 'settings' ? 'setting-green.svg' : 'setting.svg';
}
getIcon(route.name)
const gotopage = (path) => {
    pathPrefix.value = path;
    // 如果是系统设置，跳转到初始化配置页面
    if (path === 'settings') {
        router.push('/initialization');
    } else {
        router.push(`/platform/${path}`);
    }
    getIcon(path)
}

const getImgSrc = (url) => {
    return new URL(`/src/assets/img/${url}`, import.meta.url).href;
}

const mouseenteMenu = (path) => {
    if (pathPrefix.value != 'knowledgeBase' && pathPrefix.value != 'creatChat' && path != 'knowledgeBase') {
        prefixIcon.value = 'prefixIcon-grey.svg';
    }
}
const mouseleaveMenu = (path) => {
    if (pathPrefix.value != 'knowledgeBase' && pathPrefix.value != 'creatChat' && path != 'knowledgeBase') {
        getIcon(route.name)
    }
}

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

    .menu_box {
        display: flex;
        flex-direction: column;
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
    }

    .submenu {
        font-family: "PingFang SC";
        font-size: 14px;
        font-style: normal;
        font-family: "PingFang SC";
        font-size: 14px;
        font-style: normal;
        overflow-y: scroll;
        scrollbar-width: none;
        height: calc(98vh - 276px);
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
</style>