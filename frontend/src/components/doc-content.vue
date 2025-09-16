<script setup lang="ts">
import { marked } from "marked";
import { onMounted, ref, nextTick, onUnmounted, onUpdated, watch } from "vue";
import { downKnowledgeDetails } from "@/api/knowledge-base/index";
import { MessagePlugin } from "tdesign-vue-next";
import picturePreview from '@/components/picture-preview.vue';
marked.use({
  mangle: false,
  headerIds: false,
});
const renderer = new marked.Renderer();
let page = 1;
let doc = null;
let down = ref()
let mdContentWrap = ref()
let url = ref('')
let reviewUrl = ref('')
let reviewImg = ref(false)
onMounted(() => {
  nextTick(() => {
    doc = document.getElementsByClassName('t-drawer__body')[0]
    doc.addEventListener('scroll', handleDetailsScroll);
  })
})
onUpdated(() => {
  page = 1
})
onUnmounted(() => {
  doc.removeEventListener('scroll', handleDetailsScroll);
})
const checkImage = (url) => {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = url;
  });
};
renderer.image = function (href, title, text) {
  // 自定义HTML结构，图片展示带标题
  return `<figure>
                <img class="markdown-image" src="${href}" alt="${title}" title="${text}">
                <figcaption style="text-align: left;">${text}</figcaption>
            </figure>`;
};
const props = defineProps(["visible", "details"]);
const emit = defineEmits(["closeDoc", "getDoc"]);
watch(() => props.details.md, (newVal) => {
  nextTick(async () => {
    const images = mdContentWrap.value.querySelectorAll('img.markdown-image');
    if (images) {
      images.forEach(async item => {
        item.addEventListener('click', function (event) {
          reviewUrl.value = event.target.src;
          reviewImg.value = true
        })
        const isValid = await checkImage(item.src);
        if (!isValid) {
          item.remove();
        }
      })
    }
  })
}, {
  immediate: true,
  deep: true
})

// 处理 Markdown 中的图片
const processMarkdown = (markdownText) => {
  // 自定义渲染器处理图片
  marked.use({ renderer });
  let html = marked.parse(markdownText);
  const parser = new DOMParser();
  const doc = parser.parseFromString(html, 'text/html');
  return doc.body.innerHTML;
};
const closePreImg = () => {
  reviewImg.value = false
  reviewUrl.value = '';
}
const handleClose = () => {
  emit("closeDoc", false);
  doc.scrollTop = 0;
};
const downloadFile = () => {
  downKnowledgeDetails(props.details.id)
    .then((result) => {
      if (result) {
        if (url.value) {
          URL.revokeObjectURL(url.value);
        }
        url.value = URL.createObjectURL(result);
        const link = document.createElement("a");
        link.style.display = "none";
        link.setAttribute("href", url.value);
        link.setAttribute("download", props.details.title);
        link.click();
        nextTick(() => {
          document.body.removeChild(link);
          URL.revokeObjectURL(url.value);
        })
      }
    })
    .catch((err) => {
      MessagePlugin.error("获取文件失败！");
    });
};
const handleDetailsScroll = () => {
  if (doc) {
    let pageNum = Math.ceil(props.details.total / 20);
    const { scrollTop, scrollHeight, clientHeight } = doc;
    if (scrollTop + clientHeight >= scrollHeight) {
      page++;
      if (props.details.md.length < props.details.total && page <= pageNum) {
        emit("getDoc", page);
      }
    }
  }
};
</script>
<template>
  <div class="doc_content" ref="mdContentWrap">
    <t-drawer :visible="visible" :zIndex="2000" :closeBtn="true" @close="handleClose">
      <template #header>{{
        details.title.substring(0, details.title.lastIndexOf("."))
      }}</template>
      <div class="doc_box">
        <a :href="url" style="display: none" ref="down" :download="details.title"></a>
        <span class="label">文档标题</span>
        <div class="download_box">
          <span class="doc_t">{{ details.title }}</span>
          <div class="icon_box" @click="downloadFile()">
            <img class="download_box" src="@/assets/img/download.svg" alt="">
          </div>
        </div>
      </div>
      <div class="content_header">
        <span class="label">文档内容</span>
        <span class="time"> 更新时间：{{ details.time }} </span>
      </div>
      <div v-if="details.md.length == 0" class="no_content">暂无数据</div>
      <div v-else class="content" v-for="(item, index) in details.md" :key="index" :style="index % 2 !== 0
        ? 'background: #07c05f26;'
        : 'background: #3032360f;'
        ">
        <div class="md-content" v-html="processMarkdown(item.content)"></div>
      </div>
      <template #footer>
        <t-button @click="handleClose">确定</t-button>
        <t-button theme="default" @click="handleClose">取消</t-button>
      </template>
    </t-drawer>
    <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg"></picturePreview>
  </div>
</template>
<style scoped lang="less">
@import "./css/markdown.less";

:deep(.t-drawer .t-drawer__content-wrapper) {
  width: 654px !important;
}

:deep(.t-drawer__header) {
  font-weight: 800;
}

:deep(.t-drawer__body.narrow-scrollbar) {
  padding: 16px 24px;
}

.content {
  word-break: break-word;
  padding: 4px;
  gap: 4px;
  margin-top: 12px;
}

.doc_box {
  display: flex;
  flex-direction: column;
}

.label {
  color: #000000e6;
  font-size: 14px;
  font-style: normal;
  font-weight: 500;
  line-height: 22px;
  margin-bottom: 8px;
}

.download_box {
  display: flex;
  align-items: center;
}

.doc_t {
  box-sizing: border-box;
  display: flex;
  padding: 5px 8px;
  align-items: center;
  border-radius: 3px;
  border: 1px solid #dcdcdc;
  background: #30323605;
  word-break: break-all;
  text-align: justify;
}

.icon_box {
  margin-left: 18px;
  display: flex;
  overflow: hidden;
  color: #07c05f;

  .download_box {
    width: 16px;
    height: 16px;
    fill: currentColor;
    overflow: hidden;
    cursor: pointer;
  }
}

.content_header {
  margin-top: 22px;
  margin-bottom: 24px;
}

.time {
  margin-left: 12px;
  color: #00000066;
  font-size: 12px;
  font-style: normal;
  font-weight: 400;
  line-height: 20px;
}

.no_content {
  margin-top: 12px;
  color: #00000066;
  font-size: 12px;
  padding: 16px;
  background: #fbfbfb;
}
</style>
