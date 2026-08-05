<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import DataSourceEditorDialog from '@/views/knowledge/settings/DataSourceEditorDialog.vue'
import DataSourceTypeIcon from '@/views/knowledge/settings/DataSourceTypeIcon.vue'

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
    <div class="mock-page__shell">
      <section class="mock-page__intro">
        <div class="mock-page__provider">
          <span class="mock-page__provider-icon">
            <DataSourceTypeIcon type="dingtalk" :size="28" />
          </span>
          <div>
            <p class="mock-page__eyebrow">
              <span class="mock-page__status-dot" aria-hidden="true" />
              模拟演示环境
            </p>
            <h1>钉钉数据源接入流程</h1>
          </div>
        </div>

        <p class="mock-page__lead">
          使用与生产环境完全相同的数据源编辑器，验证凭证检查、资源选择和同步策略配置流程。
        </p>

        <div class="mock-page__notice">
          <t-icon name="info-circle-filled" size="18px" />
          <div>
            <strong>仅用于界面与请求链路验收</strong>
            <span>所有凭证和资源均为本地模拟数据，不会连接真实钉钉企业或发起真实同步。</span>
          </div>
        </div>

        <button v-if="!visible" type="button" class="mock-page__reopen" @click="reopen">
          <t-icon name="refresh" />
          再次验证
        </button>
      </section>

      <section
        v-if="syncCompleted"
        class="mock-receipt"
        data-testid="mock-knowledge-list"
      >
        <header class="mock-receipt__header">
          <span class="mock-receipt__success" aria-hidden="true">
            <t-icon name="check" size="18px" />
          </span>
          <div>
            <h2>配置保存成功</h2>
            <p>编辑器已完成模拟创建，并提交同步任务。</p>
          </div>
          <span class="mock-receipt__tag">事件已接收</span>
        </header>

        <div class="mock-receipt__path" aria-label="已选资源路径">
          <span>钉钉产品协作空间</span>
          <t-icon name="chevron-right" />
          <span>使用指南</span>
          <t-icon name="chevron-right" />
          <strong>钉钉产品使用手册</strong>
        </div>

        <article data-testid="mock-knowledge-item">
          <span class="mock-receipt__document-icon" aria-hidden="true">
            <t-icon name="file-paste" size="20px" />
          </span>
          <div class="mock-receipt__document-copy">
            <strong>钉钉产品使用手册</strong>
            <span>已收到“钉钉产品协作空间”的编辑器保存事件</span>
          </div>
          <span class="mock-receipt__verified">
            <t-icon name="check-circle-filled" />
            请求链路完成
          </span>
        </article>

        <p class="mock-receipt__footnote">模拟保存事件 · 不代表真实钉钉内容已同步</p>
      </section>
    </div>

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
  box-sizing: border-box;
  padding: 56px 32px;
  color: var(--td-text-color-primary);
  background:
    radial-gradient(
      circle at 78% 8%,
      color-mix(in srgb, #1677ff 10%, transparent),
      transparent 34%
    ),
    linear-gradient(
      145deg,
      color-mix(in srgb, var(--td-brand-color) 6%, var(--td-bg-color-page)),
      var(--td-bg-color-page) 54%
    );
}

.mock-page,
.mock-page * {
  box-sizing: border-box;
}

.mock-page__shell {
  width: min(780px, 100%);
  margin: 0 auto;
}

.mock-page__intro,
.mock-receipt {
  padding: 28px;
  margin-bottom: 20px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 16px;
  background: var(--td-bg-color-container);
  box-shadow: 0 16px 40px color-mix(in srgb, var(--td-text-color-primary) 8%, transparent);
}

.mock-page__provider {
  display: flex;
  align-items: center;
  gap: 14px;
}

.mock-page__provider-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  flex: 0 0 auto;
  border: 1px solid color-mix(in srgb, #1677ff 22%, var(--td-component-stroke));
  border-radius: 14px;
  background: color-mix(in srgb, #1677ff 8%, var(--td-bg-color-container));
}

.mock-page__eyebrow {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 3px;
  color: color-mix(in srgb, #1677ff 60%, var(--td-text-color-primary));
  font-size: 12px;
  font-weight: 600;
}

.mock-page__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #1677ff;
  box-shadow: 0 0 0 4px color-mix(in srgb, #1677ff 12%, transparent);
}

.mock-page h1 {
  margin: 0;
  font-size: 26px;
  font-weight: 650;
  line-height: 1.3;
  letter-spacing: -0.02em;
}

.mock-page__lead {
  max-width: 620px;
  margin: 20px 0 16px;
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1.75;
}

.mock-page__notice {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid color-mix(in srgb, #1677ff 20%, var(--td-component-stroke));
  border-radius: 10px;
  color: #1677ff;
  background: color-mix(in srgb, #1677ff 6%, var(--td-bg-color-container));
}

.mock-page__notice > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.mock-page__notice strong {
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.mock-page__notice span {
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.55;
}

.mock-page__reopen {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  margin-top: 18px;
  padding: 7px 14px;
  color: var(--td-bg-color-container);
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid var(--td-brand-color-7, var(--td-brand-color));
  border-radius: 8px;
  background: var(--td-brand-color-7, var(--td-brand-color));
  transition: background 0.15s ease, border-color 0.15s ease, transform 0.15s ease;
}

.mock-page__reopen:hover,
.mock-page__reopen:focus-visible {
  border-color: var(--td-brand-color-8, var(--td-brand-color-active));
  background: var(--td-brand-color-8, var(--td-brand-color-active));
  transform: translateY(-1px);
}

.mock-page__reopen:focus-visible {
  outline: 2px solid var(--td-brand-color-7, var(--td-brand-color));
  outline-offset: 2px;
}

.mock-receipt__header {
  display: flex;
  align-items: center;
  gap: 12px;
}

.mock-receipt__success {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  flex: 0 0 auto;
  border-radius: 11px;
  color: var(--td-success-color-7, var(--td-success-color));
  background: var(--td-success-color-1);
}

.mock-receipt__header > div {
  flex: 1;
  min-width: 0;
}

.mock-receipt h2 {
  margin: 0;
  font-size: 19px;
  font-weight: 650;
  line-height: 1.4;
}

.mock-receipt__header p {
  margin: 2px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.mock-receipt__tag {
  flex: 0 0 auto;
  padding: 4px 9px;
  border-radius: 999px;
  color: var(--td-success-color-7, var(--td-success-color));
  background: var(--td-success-color-1);
  font-size: 11px;
  font-weight: 600;
}

.mock-receipt__path {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  margin: 20px 0 10px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.mock-receipt__path strong {
  color: var(--td-text-color-secondary);
  font-weight: 500;
}

.mock-receipt article {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);
}

.mock-receipt__document-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  flex: 0 0 auto;
  border-radius: 9px;
  color: #1677ff;
  background: color-mix(in srgb, #1677ff 10%, var(--td-bg-color-container));
}

.mock-receipt__document-copy {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}

.mock-receipt__document-copy strong {
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mock-receipt__document-copy span {
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mock-receipt__verified {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: 0 0 auto;
  color: var(--td-success-color-7, var(--td-success-color));
  font-size: 12px;
  font-weight: 500;
}

.mock-receipt__footnote {
  margin: 10px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 11px;
  text-align: right;
}

@media (max-width: 720px) {
  .mock-page {
    padding: 20px 14px;
  }

  .mock-page__intro,
  .mock-receipt {
    padding: 20px;
    border-radius: 13px;
  }

  .mock-page h1 {
    font-size: 22px;
  }

  .mock-receipt__header,
  .mock-receipt article {
    align-items: flex-start;
  }

  .mock-receipt__header {
    flex-wrap: wrap;
  }

  .mock-receipt__tag {
    margin-left: 50px;
  }

  .mock-receipt article {
    flex-wrap: wrap;
  }

  .mock-receipt__verified {
    width: 100%;
    padding-left: 48px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .mock-page__reopen {
    transition: none;
  }
}
</style>
