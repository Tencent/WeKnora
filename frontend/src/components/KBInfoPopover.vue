<template>
  <t-popup
    v-if="kbInfo"
    trigger="click"
    placement="bottom-right"
    :overlay-style="{ padding: 0 }"
    :overlay-inner-style="{ padding: 0 }"
  >
    <template #content>
      <div class="kb-info-card">
        <div class="kb-info-card-header">{{ t('knowledgeBase.infoCard.title') }}</div>
        <div class="kb-info-card-body">
          <div class="kb-info-card-section">
            <div class="kb-info-card-section-title">
              {{ t('knowledgeBase.infoCard.basic') }}
            </div>
            <div class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.type') }}</span>
              <span class="kb-info-card-value">
                {{ kbInfo.type === 'faq'
                  ? t('knowledgeEditor.basic.typeFAQ')
                  : t('knowledgeEditor.basic.typeDocument') }}
              </span>
            </div>
            <div v-if="kbInfo.description" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.description') }}</span>
              <span class="kb-info-card-value kb-info-card-value-block">{{ kbInfo.description }}</span>
            </div>
            <div v-if="kbInfo.created_at" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.createdAt') }}</span>
              <span class="kb-info-card-value">{{ formatStringDate(new Date(kbInfo.created_at)) }}</span>
            </div>
            <div v-if="hasDistinctUpdate" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.accessInfo.lastUpdated') }}</span>
              <span class="kb-info-card-value">{{ lastUpdatedLabel }}</span>
            </div>
            <div v-if="supportedFileTypesSorted.length" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.supportedFileTypes') }}</span>
              <span class="kb-info-card-value">
                <span
                  v-for="ft in supportedFileTypesSorted"
                  :key="ft"
                  class="kb-info-card-ext"
                >.{{ ft }}</span>
              </span>
            </div>
          </div>
          <div class="kb-info-card-section">
            <div class="kb-info-card-section-title">
              {{ t('knowledgeBase.infoCard.access') }}
            </div>
            <div class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.accessInfo.myRole') }}</span>
              <span class="kb-info-card-value">
                <t-tag size="small" :theme="roleTagTheme">
                  {{ accessRoleLabel }}
                </t-tag>
                <span class="kb-info-card-hint">{{ accessPermissionSummary }}</span>
              </span>
            </div>
            <div v-if="currentSharedKb" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.accessInfo.fromOrg') }}</span>
              <span class="kb-info-card-value">
                「{{ currentSharedKb.org_name }}」 · {{ t('knowledgeBase.accessInfo.sharedAt') }}
                {{ formatStringDate(new Date(currentSharedKb.shared_at)) }}
              </span>
            </div>
            <div v-else-if="effectiveKBPermission" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.source') }}</span>
              <span class="kb-info-card-value">{{ t('knowledgeList.detail.sourceTypeAgent') }}</span>
            </div>
            <div v-if="(kbInfo.share_count ?? 0) > 0" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.sharedTo') }}</span>
              <span class="kb-info-card-value">
                {{ t('knowledgeList.sharedToOrgs', { count: kbInfo.share_count }) }}
              </span>
            </div>
          </div>
          <div v-if="capabilities.length" class="kb-info-card-section">
            <div class="kb-info-card-section-title">
              {{ t('knowledgeBase.infoCard.capabilities') }}
            </div>
            <div class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.enabled') }}</span>
              <span class="kb-info-card-value">
                <t-tag
                  v-for="cap in capabilities"
                  :key="cap.key"
                  size="small"
                  variant="light"
                  :theme="cap.theme"
                >
                  {{ cap.label }}
                </t-tag>
              </span>
            </div>
          </div>
          <div v-if="chunkingRows.length" class="kb-info-card-section">
            <div class="kb-info-card-section-title">
              {{ t('knowledgeBase.infoCard.chunking') }}
            </div>
            <div
              v-for="row in chunkingRows"
              :key="row.key"
              class="kb-info-card-row"
            >
              <span class="kb-info-card-label">{{ row.label }}</span>
              <span class="kb-info-card-value">{{ row.value }}</span>
            </div>
          </div>
          <div v-if="statRows.length" class="kb-info-card-section">
            <div class="kb-info-card-section-title">
              {{ t('knowledgeBase.infoCard.stats') }}
            </div>
            <div
              v-for="stat in statRows"
              :key="stat.key"
              class="kb-info-card-row"
            >
              <span class="kb-info-card-label">{{ stat.label }}</span>
              <span class="kb-info-card-value kb-info-card-value-number">{{ stat.value }}</span>
            </div>
          </div>
          <div
            v-if="kbInfo.vector_store_source || kbInfo.storage_provider_config?.provider"
            class="kb-info-card-section"
          >
            <div class="kb-info-card-section-title">
              {{ t('knowledgeBase.infoCard.binding') }}
            </div>
            <div v-if="kbInfo.vector_store_source" class="kb-info-card-row">
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.vectorStore') }}</span>
              <span class="kb-info-card-value">
                <VectorStoreBadge
                  :source="kbInfo.vector_store_source"
                  :name="kbInfo.vector_store_name"
                  :engine-type="kbInfo.vector_store_engine_type"
                  :status="kbInfo.vector_store_status"
                />
              </span>
            </div>
            <div
              v-if="kbInfo.storage_provider_config?.provider"
              class="kb-info-card-row"
            >
              <span class="kb-info-card-label">{{ t('knowledgeBase.infoCard.fileStorage') }}</span>
              <span class="kb-info-card-value kb-info-card-value-mono">
                {{ kbInfo.storage_provider_config.provider }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </template>
    <t-tooltip :content="t('knowledgeBase.infoCard.tooltip')" placement="top">
      <button
        type="button"
        class="kb-info-button"
        :class="{ 'has-warning': kbInfo?.vector_store_status === 'unavailable' }"
      >
        <t-icon name="info-circle" size="16px" />
      </button>
    </t-tooltip>
  </t-popup>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import VectorStoreBadge from '@/components/VectorStoreBadge.vue'
import { useOrganizationStore } from '@/stores/organization'
import { useAuthStore } from '@/stores/auth'
import { formatStringDate } from '@/utils'

const props = defineProps<{
  // The KB detail / list object. Typed as any to avoid coupling to the
  // backend's exhaustive shape; the popover reads optional fields and
  // falls back gracefully when they are missing.
  kbInfo: any
  // Optional pre-computed list of supported file extensions (e.g.
  // ["pdf", "docx", …]). The popover does not fetch parser engines on
  // its own — call sites that already know which formats are reachable
  // pass them in; FAQ KBs simply omit the prop.
  supportedFileTypes?: string[]
}>()

const { t } = useI18n()
const orgStore = useOrganizationStore()
const authStore = useAuthStore()

// "Owner" here mirrors the per-page guards: the original creator
// (creator_id) — not "in my tenant". creator_id is unset for legacy
// KBs created before that gate existed; those fall through to the
// role/share check.
const isOwner = computed<boolean>(() => {
  const kb = props.kbInfo
  if (!kb) return false
  const creatorId = kb.creator_id || ''
  const userId = authStore.user?.id || ''
  if (!creatorId) return false
  return creatorId === userId
})

const currentSharedKb = computed(() => {
  const id = props.kbInfo?.id
  if (!id) return null
  return orgStore.sharedKnowledgeBases?.find?.((s: any) => s.knowledge_base?.id === id) ?? null
})

const isViaShare = computed<boolean>(() => !!currentSharedKb.value)

const effectiveKBPermission = computed<string>(() => {
  const id = props.kbInfo?.id || ''
  return orgStore.getKBPermission?.(id) || props.kbInfo?.my_permission || ''
})

const accessRoleLabel = computed<string>(() => {
  if (!isViaShare.value && isOwner.value) return t('knowledgeBase.accessInfo.roleOwner')
  const perm = effectiveKBPermission.value
  if (perm) return t(`organization.role.${perm}`)
  return '--'
})

const accessPermissionSummary = computed<string>(() => {
  if (!isViaShare.value && isOwner.value) return t('knowledgeBase.accessInfo.permissionOwner')
  const perm = effectiveKBPermission.value
  if (perm === 'admin') return t('knowledgeBase.accessInfo.permissionAdmin')
  if (perm === 'editor') return t('knowledgeBase.accessInfo.permissionEditor')
  if (perm === 'viewer') return t('knowledgeBase.accessInfo.permissionViewer')
  return '--'
})

type RoleTheme = 'success' | 'primary' | 'warning' | 'default'
const roleTagTheme = computed<RoleTheme>(() => {
  if (!isViaShare.value && isOwner.value) return 'success'
  const perm = effectiveKBPermission.value
  if (perm === 'admin') return 'primary'
  if (perm === 'editor') return 'warning'
  return 'default'
})

// KB.UpdatedAt is auto-bumped by GORM only when the KB row itself is
// touched (rename, config edit, …). For a freshly created KB whose
// only mutations were document uploads, updated_at == created_at and
// surfacing "last updated" alongside "created at" reads as
// duplicated noise. Hide the row in that case and let it reappear
// once the KB metadata is actually edited.
const hasDistinctUpdate = computed<boolean>(() => {
  const created = props.kbInfo?.created_at
  const updated = props.kbInfo?.updated_at
  if (!updated) return false
  if (!created) return true
  return new Date(updated).getTime() !== new Date(created).getTime()
})

const lastUpdatedLabel = computed<string>(() => {
  const raw = props.kbInfo?.updated_at
  return raw ? formatStringDate(new Date(raw)) : ''
})

const supportedFileTypesSorted = computed<string[]>(() => {
  if (!props.supportedFileTypes?.length) return []
  return [...props.supportedFileTypes].sort()
})

type CapabilityTheme = 'primary' | 'success' | 'warning' | 'default'
const capabilities = computed<Array<{ key: string; label: string; theme: CapabilityTheme }>>(() => {
  const kb: any = props.kbInfo
  if (!kb) return []
  const items: Array<{ key: string; label: string; theme: CapabilityTheme }> = []
  if (kb.vlm_config?.enabled) {
    items.push({ key: 'vlm', label: 'VLM', theme: 'primary' })
  }
  if (kb.asr_config?.enabled) {
    items.push({ key: 'asr', label: 'ASR', theme: 'primary' })
  }
  if (kb.extract_config?.enabled) {
    items.push({
      key: 'kg',
      label: t('knowledgeList.features.knowledgeGraph'),
      theme: 'success',
    })
  }
  if (kb.indexing_strategy?.wiki_enabled) {
    items.push({ key: 'wiki', label: 'Wiki', theme: 'warning' })
  }
  return items
})

const chunkingStrategyLabel = computed<string>(() => {
  const raw: string = (props.kbInfo?.chunking_config?.strategy || '').toLowerCase()
  const key = (raw === '' || raw === 'recursive') ? 'legacy' : raw
  const path = `knowledgeEditor.chunking.strategies.${key}.label`
  const translated = t(path)
  return translated === path ? raw : translated
})

const chunkingRows = computed<Array<{ key: string; label: string; value: string }>>(() => {
  const kb: any = props.kbInfo
  if (!kb || kb.type === 'faq') return []
  const cfg = kb.chunking_config
  if (!cfg) return []
  const rows: Array<{ key: string; label: string; value: string }> = []
  if (chunkingStrategyLabel.value) {
    rows.push({
      key: 'strategy',
      label: t('knowledgeEditor.chunking.strategyLabel'),
      value: chunkingStrategyLabel.value,
    })
  }
  const chars = t('knowledgeEditor.chunking.characters')
  if (typeof cfg.chunk_size === 'number' && cfg.chunk_size > 0) {
    rows.push({
      key: 'size',
      label: t('knowledgeEditor.chunking.sizeLabel'),
      value: `${cfg.chunk_size} ${chars}`,
    })
  }
  if (typeof cfg.chunk_overlap === 'number') {
    rows.push({
      key: 'overlap',
      label: t('knowledgeEditor.chunking.overlapLabel'),
      value: `${cfg.chunk_overlap} ${chars}`,
    })
  }
  if (cfg.enable_parent_child) {
    const parent = cfg.parent_chunk_size || 4096
    const child = cfg.child_chunk_size || 384
    rows.push({
      key: 'parent-child',
      label: t('knowledgeEditor.chunking.parentChildLabel'),
      value: `${t('knowledgeBase.infoCard.parentShort')} ${parent} / ${t('knowledgeBase.infoCard.childShort')} ${child}`,
    })
  }
  if (typeof cfg.token_limit === 'number' && cfg.token_limit > 0) {
    rows.push({
      key: 'token-limit',
      label: t('knowledgeEditor.chunking.tokenLimitLabel'),
      value: String(cfg.token_limit),
    })
  }
  return rows
})

const statRows = computed<Array<{ key: string; label: string; value: number | string }>>(() => {
  const kb: any = props.kbInfo
  if (!kb) return []
  const items: Array<{ key: string; label: string; value: number | string }> = []
  // FAQ KBs store every Q/A pair as a chunk, so chunk_count is the
  // user-facing entry total. Document KBs use knowledge_count for the
  // file-level total (chunk_count there counts internal splits and is
  // not meaningful to surface here). Mirrors the same branching used
  // by the list card.
  if (kb.type === 'faq') {
    if (typeof kb.chunk_count === 'number') {
      items.push({
        key: 'faq',
        label: t('knowledgeBase.infoCard.faqCount'),
        value: kb.chunk_count,
      })
    }
  } else if (typeof kb.knowledge_count === 'number') {
    items.push({
      key: 'knowledge',
      label: t('knowledgeBase.infoCard.documentCount'),
      value: kb.knowledge_count,
    })
  }
  return items
})
</script>

<style scoped lang="less">
.kb-info-button {
  position: relative;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: all 0.2s ease;
  padding: 0;

  &:hover:not(:disabled) {
    background: var(--td-success-color-light);
    color: var(--td-brand-color);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }

  &.has-warning::after {
    content: '';
    position: absolute;
    top: 4px;
    right: 4px;
    width: 8px;
    height: 8px;
    background: var(--td-error-color);
    border-radius: 50%;
    border: 1.5px solid var(--td-bg-color-secondarycontainer);
  }

  :deep(.t-icon) {
    font-size: 18px;
  }
}

.kb-info-card {
  min-width: 280px;
  max-width: 360px;
  /* Cap to ~70% of the viewport so the popup never overflows the screen
     on shorter laptops. Internal scroll keeps the header pinned via the
     sticky rule below. */
  max-height: min(70vh, 560px);
  display: flex;
  flex-direction: column;
  padding: 12px 16px;
  font-size: 12px;
  color: var(--td-text-color-primary);
  overflow: hidden;
}

.kb-info-card-header {
  flex: 0 0 auto;
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--td-component-stroke);
}

/* Scrolling body so the popover never grows past max-height. Negative
   side margins line the scrollbar up with the card edge while keeping
   the row labels aligned with the header. */
.kb-info-card-body {
  flex: 1 1 auto;
  overflow-y: auto;
  margin: 0 -16px;
  padding: 0 16px;
}

.kb-info-card-section + .kb-info-card-section {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px dashed var(--td-component-stroke);
}

.kb-info-card-section-title {
  font-size: 11px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}

.kb-info-card-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 3px 0;
  line-height: 1.6;
}

.kb-info-card-label {
  flex: 0 0 80px;
  color: var(--td-text-color-secondary);
}

.kb-info-card-value {
  flex: 1;
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.kb-info-card-value-block {
  display: block;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
}

.kb-info-card-value-number {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.kb-info-card-value-mono {
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

.kb-info-card-ext {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--td-bg-color-component, #f5f7fa);
  color: var(--td-text-color-secondary);
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 11px;
  line-height: 1.4;
}

.kb-info-card-hint {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}
</style>
