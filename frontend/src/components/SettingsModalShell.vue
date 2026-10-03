<template>
  <Teleport to="body">
    <Transition name="settings-modal-shell">
      <div v-if="visible" class="settings-modal-shell settings-overlay" :class="overlayClass" :style="{ zIndex }"
        @click.self="emit('close')">
        <div class="settings-modal">
          <div v-if="loading" class="editor-initializing" role="status" :aria-label="$t('common.loading')">
            <t-loading size="medium" :text="$t('common.loading')" />
          </div>

          <button class="close-btn" type="button" @click="emit('close')" :aria-label="$t('common.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
            </svg>
          </button>

          <div class="settings-container">
            <aside class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ title }}</h2>
                <slot name="sidebar-header-extra" />
              </div>
              <nav class="settings-nav" :data-guide="navGuide || undefined">
                <slot name="nav">
                  <template v-for="group in navGroups" :key="group.key">
                    <div class="nav-group-title">{{ group.label }}</div>
                    <div v-for="item in group.items" :key="item.key" :class="['nav-item', { active: modelValue === item.key }]"
                      :data-guide="navItemGuidePrefix ? `${navItemGuidePrefix}-${item.key}` : undefined"
                      @click="emit('update:modelValue', item.key)">
                      <slot name="nav-icon" :item="item" :active="modelValue === item.key">
                        <t-icon :name="item.icon" class="nav-icon" />
                      </slot>
                      <span class="nav-label">{{ item.label }}</span>
                      <span v-if="showBadge(item)" :class="['nav-badge', item.badgeClass]">{{ item.badge }}</span>
                    </div>
                  </template>
                </slot>
              </nav>
            </aside>

            <div class="settings-content">
              <div class="settings-body">
                <slot />
              </div>
              <div v-if="$slots.footer || $slots['footer-note']" class="settings-footer">
                <slot name="footer-note" />
                <div class="settings-footer-actions">
                  <slot name="footer" />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * 全屏"设置类"弹窗壳：遮罩 + 1080×780 面板 + 左侧分组导航 + 右侧内容 + 可选底栏。
 * 被 Settings / AgentEditorModal / OrganizationSettingsModal / KnowledgeBaseEditorModal 共用，
 * 关闭行为（Esc / 遮罩 / 未保存守卫）由消费方通过 useModalShell 决定，本组件只发出 `close`。
 *
 * 样式为非 scoped、以 `.settings-modal-shell` 为根前缀，这样通过 `nav` 插槽自定义导航的消费方
 * 也能复用 .nav-item / .nav-icon 等基础样式。
 */
export interface SettingsModalNavItem {
  key: string
  label: string
  icon?: string
  badge?: number | string | null
  /** 额外的徽标 class，例如 'nav-badge-count' */
  badgeClass?: string
  /** 徽标为 0 时是否仍显示 */
  showZeroBadge?: boolean
  [extra: string]: unknown
}

export interface SettingsModalNavGroup {
  key: string
  label: string
  items: SettingsModalNavItem[]
}

withDefaults(
  defineProps<{
    visible: boolean
    title: string
    /** 当前分区 key（v-model） */
    modelValue?: string
    navGroups?: SettingsModalNavGroup[]
    loading?: boolean
    zIndex?: number
    overlayClass?: string
    navGuide?: string
    navItemGuidePrefix?: string
  }>(),
  {
    modelValue: '',
    navGroups: () => [],
    loading: false,
    zIndex: 1100,
    overlayClass: '',
    navGuide: '',
    navItemGuidePrefix: '',
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', key: string): void
  (e: 'close'): void
}>()

function showBadge(item: SettingsModalNavItem): boolean {
  if (item.badge == null || item.badge === '') return false
  if (typeof item.badge === 'number' && item.badge <= 0) return !!item.showZeroBadge
  return true
}
</script>

<style lang="less">
.settings-modal-shell.settings-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  backdrop-filter: blur(4px);
  overscroll-behavior: none;
}

.settings-modal-shell {
  .settings-modal {
    position: relative;
    width: 100%;
    // 1080×780：给成员表 / 系统设置这类多列内容留足空间；外层 20px padding 后 1120，
    // 1280+ 的笔记本都放得下；更窄的视口由 width: 100% 收缩。
    max-width: 1080px;
    height: 780px;
    max-height: calc(100vh - 40px);
    background: var(--td-bg-color-container);
    border-radius: var(--app-radius-xl);
    box-shadow: var(--td-shadow-3);
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .editor-initializing {
    position: absolute;
    inset: 0;
    z-index: 20;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--td-bg-color-container);
  }

  .close-btn {
    position: absolute;
    top: 16px;
    right: 16px;
    width: 32px;
    height: 32px;
    border: none;
    background: transparent;
    border-radius: var(--app-radius-sm);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--td-text-color-secondary);
    transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;
    z-index: 10;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }
  }

  .settings-container {
    display: flex;
    height: 100%;
    width: 100%;
    overflow: hidden;
  }

  .settings-sidebar {
    width: 208px;
    background-color: var(--td-bg-color-settings-modal);
    border-right: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .sidebar-header {
    padding: 16px 14px 12px;
    border-bottom: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
  }

  .sidebar-title {
    margin: 0;
    font-size: var(--app-text-xl);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .settings-nav {
    flex: 1;
    padding: 8px 8px 12px;
    overflow-y: auto;
    min-height: 0;

    &::-webkit-scrollbar {
      width: 6px;
    }

    &::-webkit-scrollbar-track {
      background: var(--td-bg-color-secondarycontainer);
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-gray-color-5);
      border-radius: 3px;
    }

    &::-webkit-scrollbar-thumb:hover {
      background: var(--td-gray-color-6);
    }
  }

  .nav-group-title {
    padding: 8px 14px 2px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
    font-weight: 600;
    letter-spacing: 0.02em;
  }

  .settings-nav > .nav-group-title:first-child {
    padding-top: 2px;
  }

  .nav-item {
    display: flex;
    align-items: center;
    padding: 6px 12px;
    margin-bottom: 2px;
    border-radius: var(--app-radius-sm);
    cursor: pointer;
    transition: background-color var(--app-motion-base) ease, color var(--app-motion-base) ease;
    font-size: var(--app-text-base);
    color: var(--td-text-color-primary);
    user-select: none;

    &:hover {
      background-color: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }

    &.active {
      background-color: var(--td-bg-color-secondarycontainer);
      color: var(--td-brand-color);
      font-weight: 500;
    }
  }

  .nav-icon {
    margin-right: 9px;
    font-size: var(--app-text-xl);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: inherit;

    &.nav-icon-img {
      width: 16px;
      height: 16px;
    }

    &.nav-icon-emoji {
      font-size: var(--app-text-base);
      line-height: 1;
    }
  }

  .nav-label {
    flex: 1;
  }

  .nav-badge {
    flex-shrink: 0;
    margin-left: 2px;
    padding: 0 6px;
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xs);
    line-height: 16px;
    font-weight: 500;
    text-align: center;

    &.nav-badge-count {
      min-width: 20px;
    }
  }

  .settings-content {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
    background-color: var(--td-bg-color-container);
  }

  // 既是滚动容器，也是 flex 列：消费方的 .content-wrapper 想自己滚动时设 flex:1 + overflow:auto，
  // 想让整块内容随页面滚动时保持自然高度即可。
  .settings-body {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow-y: auto;

    &::-webkit-scrollbar {
      width: 6px;
    }

    &::-webkit-scrollbar-track {
      background: var(--td-bg-color-container);
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-gray-color-5);
      border-radius: 3px;
    }

    &::-webkit-scrollbar-thumb:hover {
      background: var(--td-gray-color-6);
    }
  }

  .settings-footer {
    padding: 12px 40px;
    border-top: 1px solid var(--td-component-stroke);
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 16px;
    flex-shrink: 0;
    background-color: var(--td-bg-color-container);
  }

  .settings-footer-note {
    margin: 0;
    margin-right: auto;
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: flex-start;
    gap: 6px;
    font-size: var(--app-text-md);
    line-height: 20px;
    color: var(--td-text-color-secondary);

    strong {
      margin-right: 4px;
      color: var(--td-text-color-primary);
      font-weight: 500;
    }

    &__icon {
      flex-shrink: 0;
      margin-top: 2px;
      font-size: var(--app-text-base);
      color: var(--td-success-color);
    }
  }

  .settings-footer-actions {
    display: flex;
    gap: 12px;
    flex-shrink: 0;
  }
}

.settings-modal-shell-leave-active {
  transition: opacity var(--app-motion-base) ease;

  .settings-modal {
    transition: transform var(--app-motion-base) ease, opacity var(--app-motion-base) ease;
  }
}

.settings-modal-shell-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.95);
    opacity: 0;
  }
}
</style>
