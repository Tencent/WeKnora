<template>
  <span :class="['vs-badge', `vs-badge-${effectiveSource}`, isUnavailable && 'vs-badge-warn']">
    <t-icon :name="iconName" class="vs-badge-icon" />
    <span class="vs-badge-name">{{ displayName }}</span>
    <span
      v-if="engineType && (effectiveSource === 'user' || effectiveSource === 'env')"
      class="vs-badge-engine"
    >
      ({{ engineType }})
    </span>
    <t-tag
      v-if="isUnavailable"
      theme="danger"
      variant="light"
      size="small"
      class="vs-badge-warn-tag"
    >
      {{ $t('vectorStoreBadge.unavailable') }}
    </t-tag>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { VectorStoreSource, VectorStoreStatus } from '@/api/knowledge-base'

const props = defineProps<{
  source?: VectorStoreSource
  name?: string
  engineType?: string
  status?: VectorStoreStatus
}>()

const { t } = useI18n()

// When backend omits the source (e.g. legacy KB row from a cached list
// endpoint that does not enrich), treat it as env so the badge renders
// gracefully instead of going blank.
const effectiveSource = computed<VectorStoreSource>(() => props.source || 'env')

const isUnavailable = computed(
  () => props.status === 'unavailable' || effectiveSource.value === 'unavailable',
)

const iconName = computed(() => {
  switch (effectiveSource.value) {
    case 'env':
    case 'user':
      // Both env- and user-bound KBs sit on top of a vector store; the
      // distinction is purely organizational (configured at process
      // start vs. created in the UI), so they share the same icon.
      return 'data-base'
    case 'shared':
      return 'share'
    case 'unavailable':
    default:
      return 'help-circle'
  }
})

const displayName = computed(() => {
  if (effectiveSource.value === 'env') return t('vectorStoreBadge.systemDefault')
  if (effectiveSource.value === 'shared') return t('vectorStoreBadge.sharedFromOrg')
  return props.name || t('vectorStoreBadge.unknownStore')
})
</script>

<style scoped>
.vs-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-sm);
  line-height: 1.4;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-primary);
}

.vs-badge-env {
  background: var(--td-brand-color-1);
  color: var(--td-brand-color-7);
}

.vs-badge-user {
  background: var(--td-success-color-1);
  color: var(--td-success-color-7);
}

.vs-badge-shared {
  background: var(--td-warning-color-1);
  color: var(--td-warning-color-7);
}

.vs-badge-warn {
  background: var(--td-error-color-1);
  color: var(--td-error-color-7);
}

.vs-badge-icon {
  font-size: var(--app-text-base);
}

.vs-badge-engine {
  opacity: 0.7;
  font-size: var(--app-text-xs);
}

.vs-badge-warn-tag {
  margin-left: 4px;
}
</style>
