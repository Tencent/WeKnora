<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="handleClose">
        <div class="settings-modal">
          <div class="settings-container">
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ $t('integrations.title') }}</h2>
                <p class="sidebar-subtitle">{{ $t('integrations.subtitle') }}</p>
              </div>
              <div class="settings-nav">
                <div
                  v-for="item in navItems"
                  :key="item.key"
                  :class="['nav-item', { active: currentSection === item.key }]"
                  @click="currentSection = item.key"
                >
                  <span v-if="item.emoji" class="nav-emoji" role="img" :aria-label="item.label">{{ item.emoji }}</span>
                  <t-icon v-else :name="item.icon" class="nav-icon" />
                  <span class="nav-label">{{ item.label }}</span>
                </div>
              </div>
            </div>

            <div class="settings-content">
              <button class="close-btn" @click="handleClose" :aria-label="$t('common.close')">
                <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                  <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
                </svg>
              </button>
              <div class="content-wrapper" :class="{ 'content-wrapper--landing': isLandingSection }">
                <div v-if="currentSection === 'im'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agentEditor.im.title') }}</h2>
                    <p class="section-description">
                      {{ $t('agentEditor.im.description') }}
                      <a
                        href="https://github.com/Tencent/WeKnora/blob/main/docs/IM%E9%9B%86%E6%88%90%E5%BC%80%E5%8F%91%E6%96%87%E6%A1%A3.md"
                        target="_blank"
                        rel="noopener noreferrer"
                        class="doc-link"
                      >
                        {{ $t('agentEditor.im.docLink') }}
                        <t-icon name="link" class="link-icon" />
                      </a>
                    </p>
                  </div>
                  <IMChannelPanel v-model:filter-agent-id="filterAgentId" />
                </div>

                <div v-if="currentSection === 'embed'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('agentEditor.embed.title') }}</h2>
                    <p class="section-description">{{ $t('agentEditor.embed.description') }}</p>
                  </div>
                  <AgentEmbedChannelPanel v-model:filter-agent-id="filterAgentId" />
                </div>

                <div v-if="currentSection === 'api'" class="section">
                  <div class="section-header">
                    <h2>{{ $t('integrations.api.title') }}</h2>
                    <p class="section-description">{{ $t('integrations.api.subtitle') }}</p>
                  </div>
                  <ApiIntegrationSettings />
                </div>
                <ChromeExtensionLanding v-if="currentSection === 'chrome'" />
                <ClawSkillLanding v-if="currentSection === 'claw'" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import IMChannelPanel from '@/components/IMChannelPanel.vue';
import AgentEmbedChannelPanel from '@/components/AgentEmbedChannelPanel.vue';
import ApiIntegrationSettings from '@/views/integrations/ApiIntegrationSettings.vue';
import ChromeExtensionLanding from '@/views/integrations/ChromeExtensionLanding.vue';
import ClawSkillLanding from '@/views/integrations/ClawSkillLanding.vue';
import {
  INTEGRATION_PREVIEW_ITEMS,
  INTEGRATION_TAB_MIN_ROLE,
  INTEGRATION_TABS,
  type IntegrationTab,
} from '@/config/integrations';
import { useAuthStore } from '@/stores/auth';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const authStore = useAuthStore();

const currentSection = ref<IntegrationTab>('im');
const filterAgentId = ref('');

const visible = computed(() => route.name === 'integrations');

const isLandingSection = computed(
  () => currentSection.value === 'chrome' || currentSection.value === 'claw',
);

function canSeeTab(tab: IntegrationTab): boolean {
  const min = INTEGRATION_TAB_MIN_ROLE[tab];
  if (!min) return true;
  if (authStore.canAccessAllTenants) return true;
  return authStore.hasRole(min);
}

const navItems = computed(() =>
  INTEGRATION_PREVIEW_ITEMS
    .filter((item) => canSeeTab(item.key))
    .map((item) => ({
      key: item.key,
      icon: item.icon.type === 'icon' ? item.icon.name : '',
      emoji: item.icon.type === 'emoji' ? item.icon.value : undefined,
      label: t(`integrations.tabs.${item.key}`),
    })),
);

function applyRouteQuery() {
  const tab = route.query.tab as string;
  if (INTEGRATION_TABS.includes(tab as IntegrationTab) && canSeeTab(tab as IntegrationTab)) {
    currentSection.value = tab as IntegrationTab;
  } else if (INTEGRATION_TABS.includes(tab as IntegrationTab)) {
    currentSection.value = navItems.value[0]?.key ?? 'im';
    if (visible.value) syncRouteQuery();
  } else if (navItems.value.length > 0 && !canSeeTab(currentSection.value)) {
    currentSection.value = navItems.value[0].key;
    if (visible.value) syncRouteQuery();
  }
  filterAgentId.value = (route.query.agentId as string) || '';
}

function syncRouteQuery() {
  const query: Record<string, string> = { tab: currentSection.value };
  if (filterAgentId.value) {
    query.agentId = filterAgentId.value;
  }
  router.replace({ path: route.path, query });
}

function handleClose() {
  if (route.name !== 'integrations') return;
  if (window.history.length > 1) {
    router.back();
  } else {
    router.push('/platform/knowledge-bases');
  }
}

watch(visible, (open) => {
  if (open) applyRouteQuery();
});

watch(currentSection, () => {
  if (visible.value) syncRouteQuery();
});

watch(filterAgentId, () => {
  if (visible.value) syncRouteQuery();
});

watch(
  () => [route.query.tab, route.query.agentId],
  () => {
    if (visible.value) applyRouteQuery();
  },
);
</script>

<style scoped lang="less">
.settings-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.settings-modal {
  position: relative;
  width: 90vw;
  max-width: 1100px;
  height: 85vh;
  max-height: 750px;
  background: var(--td-bg-color-container);
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.settings-content {
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.close-btn {
  position: absolute;
  top: 12px;
  right: 12px;
  width: 32px;
  height: 32px;
  border: none;
  background: var(--td-bg-color-container);
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  transition: all 0.2s ease;
  z-index: 10;
  box-shadow: 0 0 0 1px var(--td-component-stroke);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding: 24px 28px 28px;

  &--landing {
    padding-right: 52px;
    padding-bottom: 20px;
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
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.sidebar-subtitle {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-placeholder);
}

.settings-nav {
  flex: 1;
  padding: 8px;
  overflow-y: auto;
  min-height: 0;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 6px 12px;
  margin-bottom: 2px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  font-size: 14px;
  color: var(--td-text-color-primary);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    font-weight: 500;

    .nav-icon {
      color: var(--td-brand-color);
    }
  }
}

.nav-icon {
  margin-right: 8px;
  font-size: 16px;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}

.nav-emoji {
  margin-right: 8px;
  font-size: 15px;
  line-height: 1;
  flex-shrink: 0;
  width: 16px;
  text-align: center;
}

.nav-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.section-header {
  margin-bottom: 20px;

  h2 {
    margin: 0 0 8px;
    font-size: 18px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
}

.section-description {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);

  .doc-link {
    color: var(--td-brand-color);
    text-decoration: none;
    display: inline-flex;
    align-items: center;
    gap: 4px;

    &:hover {
      text-decoration: underline;
    }
  }

  .link-icon {
    font-size: 14px;
  }
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;

  .settings-modal {
    transition: transform 0.2s ease, opacity 0.2s ease;
  }
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.98);
    opacity: 0;
  }
}

// ============================================================
// 移动端适配
// ============================================================
@media screen and (max-width: 767px) {
  .settings-overlay {
    padding: 0;
    align-items: stretch;
  }

  .settings-modal {
    width: 100%;
    max-width: 100%;
    height: 100%;
    max-height: 100vh;
    border-radius: 0;
  }

  .settings-container {
    flex-direction: column;
  }

  .settings-sidebar {
    width: 100%;
    flex-shrink: 0;
    border-right: none;
    border-bottom: 1px solid var(--td-component-stroke);
    overflow: visible;
    background: var(--td-bg-color-container);
  }

  .sidebar-header {
    display: none;
  }

  .settings-nav {
    display: flex;
    flex-direction: row;
    overflow-x: auto;
    overflow-y: hidden;
    padding: 10px 12px;
    gap: 2px;
    -webkit-overflow-scrolling: touch;
    scrollbar-width: none;

    &::-webkit-scrollbar { display: none; }
  }

  .nav-group-title { display: none; }

  .nav-item {
    flex-shrink: 0;
    white-space: nowrap;
    padding: 7px 12px;
    margin-bottom: 0;
    border-radius: 20px;
    font-size: 13px;
    min-height: 36px;

    .nav-icon { margin-right: 4px; font-size: 15px; }

    &.active {
      background: var(--td-brand-color-light);
      color: var(--td-brand-color);
    }
  }

  .settings-content { flex: 1; }

  .content-wrapper {
    max-width: 100%;
    padding: 20px 16px;
  }

  .close-btn {
    top: 8px;
    right: 8px;
    z-index: 20;
    width: 36px;
    height: 36px;
    background: var(--td-bg-color-container);
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.1);
    border-radius: 50%;
  }
}

// 平板适配
@media screen and (min-width: 768px) and (max-width: 1023px) {
  .settings-overlay { padding: 10px; }

  .settings-modal {
    width: 95vw;
    max-width: 100%;
    height: calc(100vh - 20px);
    max-height: none;
  }

  .settings-sidebar { width: 160px; }

  .content-wrapper {
    max-width: 100%;
    padding: 24px 20px;
  }

  .sidebar-header { padding: 14px 10px 10px; }
  .nav-item { padding: 5px 10px; font-size: 13px; }
  .nav-icon { margin-right: 5px; font-size: 14px; }
}
</style>
