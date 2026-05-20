<template>
  <span class="resource-origin-badge" :class="variantClass" :title="tooltipText">
    <t-icon :name="iconName" size="12px" class="badge-icon" />
    <span class="badge-text">{{ displayText }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Icon as TIcon } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'

/**
 * ResourceOriginBadge – a unified, compact label that explains *where* a
 * KB or Agent comes from. Replaces the ad-hoc "我的" / "shared-by-me-badge"
 * / org_name pills scattered across KnowledgeBaseList and AgentList. The
 * variants below cover the five origin shapes the list views actually
 * surface; future origins (e.g. "system" / "imported") should add a new
 * variant rather than re-using one of these.
 *
 * Variants:
 *  - mine        : created by the current user in the current tenant
 *  - tenant      : owned by the current tenant but created by someone else
 *                  — label shows tenant name; use when context doesn't say
 *  - creator     : same data shape as `tenant`, but the surrounding section
 *                  header already names the tenant ("本空间 · 仅查看"), so
 *                  the badge only carries the creator name to avoid the
 *                  duplicated "本空间 / wizardchen's Workspace" pill on
 *                  every card. Falls back to the i18n label when the
 *                  creator name is unknown.
 *  - space       : reached through a cross-tenant space (organization)
 *  - shared      : cross-tenant share without a useful org name to show
 *
 * Pass `creatorName` to surface "by 张三" in the tooltip for the `tenant`
 * variant, or to drive the visible label of the `creator` variant; omit it
 * for the `mine` / `space` / `shared` variants where the subject is implicit.
 */
const props = withDefaults(
  defineProps<{
    variant: 'mine' | 'tenant' | 'creator' | 'space' | 'shared'
    /** Used in `space` variant — the organization (space) display name. */
    spaceName?: string
    /** Optional creator display name, surfaces in tooltip for `tenant` variant. */
    creatorName?: string
    /** Optional source tenant name, surfaces in tooltip for cross-tenant. */
    sourceTenantName?: string
  }>(),
  { spaceName: '', creatorName: '', sourceTenantName: '' }
)

const { t } = useI18n()
const authStore = useAuthStore()

const iconName = computed(() => {
  switch (props.variant) {
    case 'mine':
      return 'user'
    case 'tenant':
      return 'usergroup'
    case 'creator':
      return 'user'
    case 'space':
      return 'building'
    case 'shared':
      return 'share'
    default:
      return 'usergroup'
  }
})

const variantClass = computed(() => `origin-${props.variant}`)

const displayText = computed(() => {
  switch (props.variant) {
    case 'mine':
      return t('resourceOrigin.mine')
    case 'tenant':
      // Prefer the tenant name when known so the badge says where the
      // resource lives, not a vague "tenant" label. Falls back to i18n.
      return authStore.currentTenantName || t('resourceOrigin.tenant')
    case 'creator':
      // Section header already provides the「本空间」context, so we just
      // show who created it. Fall back to a generic label when the user
      // can't be resolved (creator_name 缺失，例如已删除账号 / 老数据)。
      return props.creatorName || t('resourceOrigin.tenant')
    case 'space':
      return props.spaceName || t('resourceOrigin.space')
    case 'shared':
      return props.sourceTenantName || t('resourceOrigin.shared')
    default:
      return ''
  }
})

const tooltipText = computed(() => {
  switch (props.variant) {
    case 'mine':
      return t('resourceOrigin.mineTooltip')
    case 'tenant':
      if (props.creatorName) {
        return t('resourceOrigin.tenantTooltipWithCreator', { creator: props.creatorName })
      }
      return t('resourceOrigin.tenantTooltip')
    case 'creator':
      // 卡片标签只露名字；tooltip 把完整含义补回来。
      if (props.creatorName) {
        return t('resourceOrigin.tenantTooltipWithCreator', { creator: props.creatorName })
      }
      return t('resourceOrigin.tenantTooltip')
    case 'space':
      if (props.sourceTenantName) {
        return t('resourceOrigin.spaceTooltipWithTenant', {
          space: props.spaceName,
          tenant: props.sourceTenantName,
        })
      }
      return t('resourceOrigin.spaceTooltip', { space: props.spaceName })
    case 'shared':
      return t('resourceOrigin.sharedTooltip')
    default:
      return ''
  }
})
</script>

<style scoped lang="less">
.resource-origin-badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 1px 6px;
  border-radius: 8px;
  font-size: 11px;
  line-height: 1.4;
  font-weight: 500;
  max-width: 140px;

  .badge-icon {
    flex-shrink: 0;
  }

  .badge-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &.origin-mine {
    color: var(--td-brand-color);
    background: var(--td-success-color-light);
  }

  &.origin-tenant {
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
  }

  &.origin-creator {
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
  }

  &.origin-space {
    color: var(--td-warning-color-7, #b86e02);
    background: var(--td-warning-color-1, #fff7e6);
  }

  &.origin-shared {
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
  }
}
</style>
