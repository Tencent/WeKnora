<template>
  <div class="skill-settings">
    <div class="section-header">
      <div class="section-header__title-row">
        <h2>{{ $t('settings.skills.title') }}</h2>
        <t-tooltip
          :content="$t('settings.skills.helpTooltip')"
          placement="right"
          overlay-class-name="skill-settings__help-tooltip"
        >
          <t-icon
            name="help-circle"
            class="section-header__help"
            :aria-label="$t('settings.skills.helpTooltip')"
          />
        </t-tooltip>
      </div>
      <p class="section-description">{{ $t('settings.skills.description') }}</p>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <div v-else-if="skillConfigs.length === 0" class="empty-state">
      <t-empty :description="$t('settings.skills.noConfigsDesc')" />
      <t-button theme="primary" variant="outline" @click="uiStore.openSettings('sandbox')">
        {{ $t('settings.skills.goSandboxSettings') }}
      </t-button>
    </div>

    <template v-else>
      <div v-if="skillConfigs.length > 1" class="sandbox-switcher" role="tablist">
        <button
          v-for="cfg in skillConfigs"
          :key="cfg.id"
          type="button"
          role="tab"
          class="sandbox-switcher__item"
          :class="{ 'is-active': cfg.id === selectedId }"
          :aria-selected="cfg.id === selectedId"
          @click="selectedId = cfg.id"
        >
          <SandboxBackendBadge :type="cfg.sandbox_type" size="sm" class="sandbox-switcher__badge" />
          <span class="sandbox-switcher__name" :title="cfg.name">{{ cfg.name }}</span>
        </button>
      </div>

      <SandboxSkillsPanel
        v-if="selectedRecord"
        ref="listPanel"
        :record="selectedRecord"
        mode="list"
        @updated="onPanelUpdated"
        @install="openInstall"
      />
    </template>

    <SettingDrawer
      v-model:visible="showInstall"
      :title="$t('settings.skills.installSkill')"
      :description="installDrawerDesc"
      :icon="SKILL_ICON"
      width="680px"
      :min-width="560"
      :max-width="920"
      storage-key="setting-drawer:width:skill-catalog"
      :hide-footer="true"
    >
      <SandboxSkillsPanel
        v-if="showInstall && selectedRecord"
        :record="selectedRecord"
        mode="install"
        @updated="onPanelUpdated"
        @skills-changed="reloadList"
        @installed="onInstalled"
      />
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SandboxSkillsPanel from '@/components/SandboxSkillsPanel.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { SKILL_ICON } from '@/types/mention'
import { useUIStore } from '@/stores/ui'
import {
  isNamedSandboxBackend,
  listSandboxConfigs,
  type SandboxConfigRecord,
} from '@/api/system'

const props = defineProps<{
  initialSandboxId?: string
}>()

const { t } = useI18n()
const uiStore = useUIStore()

const loading = ref(false)
const records = ref<SandboxConfigRecord[]>([])
const selectedId = ref('')
const showInstall = ref(false)
const listPanel = ref<{
  reload: () => Promise<unknown>
  revealSkill: (skillId: string) => void
} | null>(null)

const skillConfigs = computed(() =>
  records.value.filter((record) => isNamedSandboxBackend(record.sandbox_type)),
)

const selectedRecord = computed(() =>
  skillConfigs.value.find((record) => record.id === selectedId.value) || null,
)

const installDrawerDesc = computed(() => {
  const record = selectedRecord.value
  if (!record) return ''
  return t('settings.skills.installDrawerDesc', { name: record.name })
})

function applyPreferredSelection() {
  const preferred = (props.initialSandboxId || '').trim()
  if (preferred && skillConfigs.value.some((record) => record.id === preferred)) {
    selectedId.value = preferred
    return
  }
  if (selectedId.value && skillConfigs.value.some((record) => record.id === selectedId.value)) return
  selectedId.value = skillConfigs.value[0]?.id || ''
}

function openInstall() {
  if (!selectedRecord.value) return
  showInstall.value = true
}

function onPanelUpdated(record: SandboxConfigRecord) {
  records.value = records.value.map((item) => (item.id === record.id ? { ...item, ...record } : item))
}

function reloadList() {
  void listPanel.value?.reload()
}

// Server accepted the install; jump back to the list so the just-added card
// is where progress and errors show up. Reload before closing the drawer so
// the row exists when revealSkill tries to scroll to it, and skip the
// showInstall watcher's own reload for this transition.
async function onInstalled(skillId: string) {
  await listPanel.value?.reload()
  showInstall.value = false
  if (skillId) {
    await nextTick()
    listPanel.value?.revealSkill(skillId)
  }
}

async function load() {
  loading.value = true
  try {
    const res = await listSandboxConfigs()
    records.value = res?.data || []
    applyPreferredSelection()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.skills.loadFailed'))
  } finally {
    loading.value = false
  }
}

watch(() => props.initialSandboxId, () => {
  applyPreferredSelection()
})

watch(selectedId, () => {
  if (loading.value) return
  showInstall.value = false
})

watch(showInstall, (open) => {
  if (!open) reloadList()
})

onMounted(load)
</script>

<style lang="less" scoped>
.skill-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;

  &__title-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }

  &__help {
    color: var(--td-text-color-placeholder);
    font-size: 16px;
    cursor: help;
    transition: color 0.15s ease;

    &:hover {
      color: var(--td-text-color-secondary);
    }
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.6;
  }
}

:global(.skill-settings__help-tooltip .t-popup__content) {
  max-width: 340px;
  line-height: 1.55;
}

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.empty-state {
  padding: 80px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.sandbox-switcher {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  margin-bottom: 16px;
  overflow-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.sandbox-switcher__item {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 220px;
  padding: 4px 10px 4px 4px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 999px;
  cursor: pointer;
  font: inherit;
  color: var(--td-text-color-secondary);
  text-align: left;
  transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease;

  &:hover:not(.is-active) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.sandbox-switcher__badge {
  transform: scale(0.82);
  transform-origin: center;
}

.sandbox-switcher__name {
  font-size: 13px;
  font-weight: 500;
  line-height: 1.2;
  color: inherit;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

</style>

