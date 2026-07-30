<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import DataSourceEditorDialog from '@/views/knowledge/settings/DataSourceEditorDialog.vue'

const { locale } = useI18n({ useScope: 'global' })
const previousLocale = locale.value
const previousDocumentTitle = document.title

// 该开发页用于交付中文界面证据，因此不受浏览器中已保存语言的影响。
locale.value = 'zh-CN'
document.title = '钉钉数据源接入演示 · WeKnora'

const visible = ref(true)
const syncCompleted = ref(false)

onUnmounted(() => {
  locale.value = previousLocale
  document.title = previousDocumentTitle
})

function reopen() {
  syncCompleted.value = false
  visible.value = true
}

function handleSaved() {
  syncCompleted.value = true
}
</script>

<template>
  <main class="mock-page" data-testid="dingtalk-mock-e2e">
    <section class="mock-page__intro">
      <p class="mock-page__eyebrow">仅用于开发验证的模拟端到端测试</p>
      <h1>钉钉数据源接入流程</h1>
      <p>
        本页将生产环境使用的数据源编辑器连接到本地模拟接口。
        下方回执仅证明界面已触发保存事件，不代表已完成真实的钉钉数据同步。
      </p>
      <button v-if="!visible" type="button" @click="reopen">再次验证</button>
    </section>

    <section
      v-if="syncCompleted"
      class="mock-knowledge-list"
      data-testid="mock-knowledge-list"
    >
      <h2>界面保存回执</h2>
      <article data-testid="mock-knowledge-item">
        <strong>钉钉产品使用手册</strong>
        <span>已收到“钉钉产品协作空间”的编辑器保存事件</span>
      </article>
    </section>

    <DataSourceEditorDialog
      v-model:visible="visible"
      kb-id="mock-kb"
      :data-source="null"
      @saved="handleSaved"
    />
  </main>
</template>

<style scoped>
.mock-page {
  min-height: 100vh;
  padding: 48px;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-page);
}

.mock-page__intro,
.mock-knowledge-list {
  max-width: 720px;
  padding: 24px;
  margin: 0 auto 20px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 12px;
  background: var(--td-bg-color-container);
}

.mock-page__eyebrow {
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
}

.mock-page__intro button {
  padding: 7px 14px;
  color: var(--td-brand-color);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid var(--td-brand-color);
  border-radius: 6px;
  background: transparent;
}

.mock-page__intro button:hover {
  color: var(--td-text-color-anti);
  background: var(--td-brand-color);
}

.mock-knowledge-list article {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 14px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}
</style>
