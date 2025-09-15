<template>
    <div class="bot_msg">
        <div style="display: flex;flex-direction: column; gap:8px">
            <docInfo :session="session"></docInfo>
            <deepThink :deepSession="session" v-if="session.showThink"></deepThink>
        </div>
        <div ref="parentMd">
            <!-- 消息正在总结中则渲染加载gif  -->
            <img v-if="session.thinking" class="botanswer_laoding_gif" src="@/assets/img/botanswer_loading.gif"
                alt="正在总结答案……">
            <div v-for="(item, index) in processedMarkdown" :key="index">
                <img class="ai-markdown-img" @click="preview(item)" v-if="isLink(item)" :src="item" alt="">
                <div v-else class="ai-markdown-template" v-html="processMarkdown(item)"></div>
            </div>
            <div v-if="isImgLoading" class="img_loading"><t-loading size="small"></t-loading><span>加载中...</span></div>
        </div>
        <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg"></picturePreview>
    </div>
</template>
<script setup>
import { onMounted, watch, computed, ref, reactive, defineProps, nextTick } from 'vue';
import { marked } from 'marked';
import docInfo from './docInfo.vue';
import deepThink from './deepThink.vue';
import picturePreview from '@/components/picture-preview.vue';
import { sanitizeHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL } from '@/utils/security';

marked.use({
    mangle: false,
    headerIds: false,
});
const emit = defineEmits(['scroll-bottom'])
const renderer = new marked.Renderer();
let parentMd = ref()
let reviewUrl = ref('')
let reviewImg = ref(false)
let isImgLoading = ref(false);
const props = defineProps({
    // 必填项
    content: {
        type: String,
        required: false
    },
    session: {
        type: Object,
        required: false
    },
    isFirstEnter: {
        type: Boolean,
        required: false
    }
});
const processedMarkdown = ref([]);
const preview = (url) => {
    nextTick(() => {
        reviewUrl.value = url;
        reviewImg.value = true
    })
}
const removeImg = () => {
    nextTick(() => {
        const images = parentMd.value.querySelectorAll('img.ai-markdown-img');
        if (images) {
            images.forEach(async item => {
                const isValid = await checkImage(item.src);
                if (!isValid) {
                    item.remove();
                }
            })
        }
    })
}
const closePreImg = () => {
    reviewImg.value = false
    reviewUrl.value = '';
}
const debounce = (fn, delay) => {
    let timer
    return (...args) => {
        clearTimeout(timer)
        timer = setTimeout(() => fn(...args), delay)
    }
}
const checkImage = (url) => {
    return new Promise((resolve) => {
        const img = new Image();
        img.onload = () => {
            resolve(true);
        }
        img.onerror = () => resolve(false);
        img.src = url;
    });
};
// 安全地处理 Markdown 内容
const processMarkdown = (markdownText) => {
    if (!markdownText || typeof markdownText !== 'string') {
        return '';
    }
    
    // 首先对 Markdown 内容进行安全处理
    const safeMarkdown = safeMarkdownToHTML(markdownText);
    
    // 自定义安全的渲染器处理图片
    const renderer = {
        image(href, title, text) {
            // 验证图片 URL 是否安全
            if (!isValidImageURL(href)) {
                return `<p>无效的图片链接</p>`;
            }
            // 使用安全的图片创建函数
            return createSafeImage(href, text || '', title || '');
        }
    };

    marked.use({ renderer });

    // 安全地渲染 Markdown
    let html = marked.parse(safeMarkdown);

    // 使用 DOMPurify 进行最终的安全清理
    const sanitizedHTML = sanitizeHTML(html);
    
    return sanitizedHTML;
};
const handleImg = async (newVal) => {
    let index = newVal.lastIndexOf('![');
    if (index != -1) {
        isImgLoading.value = true;
        let str = newVal.slice(index)
        if (str.includes('](') && str.includes(')')) {
            processedMarkdown.value = splitMarkdownByImages(newVal)
            isImgLoading.value = false;
        } else {
            processedMarkdown.value = splitMarkdownByImages(newVal.slice(0, index))
        }
    } else {
        processedMarkdown.value = splitMarkdownByImages(newVal)
    }
    removeImg()
}
function splitMarkdownByImages(markdown) {
    const imageRegex = /!\[.*?\]\(\s*(?:<([^>]*)>|([^)\s]*))\s*(?:["'].*?["'])?\s*\)/g;
    const result = [];
    let lastIndex = 0;
    let match;

    while ((match = imageRegex.exec(markdown)) !== null) {
        const textBefore = markdown.slice(lastIndex, match.index);
        if (textBefore) result.push(textBefore);
        const url = match[1] || match[2];
        result.push(url);
        lastIndex = imageRegex.lastIndex;
    }

    const remainingText = markdown.slice(lastIndex);
    if (remainingText) result.push(remainingText);

    return result;
}
function isLink(str) {
    const trimmedStr = str.trim();
    // 正则表达式匹配常见链接格式
    const urlPattern = /^(https?:\/\/|ftp:\/\/|www\.)(?:(?:[\w-]+(?:\.[\w-]+)*)|(?:\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})|(?:\[[a-fA-F0-9:]+\]))(?::\d{1,5})?(?:[\/\w.,@?^=%&:~+#-]*[\w@?^=%&\/~+#-])?/i;
    return urlPattern.test(trimmedStr);
}

watch(() => props.content, (newVal) => {
    debounce(handleImg(newVal), 800)
}, {
    immediate: true
})

const myMarkdown = (res) => {
    return marked.parse(res, { renderer })
}

onMounted(async () => {
    processedMarkdown.value = splitMarkdownByImages(props.content);
    removeImg()
});
</script>
<style lang="less" scoped>
@import '../../../components/css/markdown.less';

:deep(.ai-markdown-template) {
    contain: content;
    line-height: 28px;
    letter-spacing: 1px;

    h1,
    h2,
    h3,
    h4 {
        line-height: 14px;
        font-size: 16px;
    }

}

.ai-markdown-img {
    border-radius: 8px;
    display: block;
    cursor: pointer;
    object-fit: scale-down;
    contain: content;
    margin-left: 16px;
    border: 0.5px solid #E7E7E7;
    max-width: 708px;
    height: 230px;
}

.bot_msg {
    background: #fff;
    border-radius: 4px;
    color: rgba(0, 0, 0, 0.9);
    font-size: 16px;
    padding: 10px 12px;
    margin-right: auto;
    max-width: 100%;
    box-sizing: border-box;
}

.botanswer_laoding_gif {
    width: 24px;
    height: 18px;
    margin-left: 16px;
}

.img_loading {
    background: #3032360f;
    height: 230px;
    width: 230px;
    color: #00000042;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-direction: column;
    font-size: 12px;
    gap: 4px;
    margin-left: 16px;
    border-radius: 8px;
}

:deep(.t-loading__gradient-conic) {
    background: conic-gradient(from 90deg at 50% 50%, #fff 0deg, #676767 360deg) !important;

}
</style>