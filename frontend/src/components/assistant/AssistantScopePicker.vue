<template>
  <div class="assistant-scope">
    <t-select :value="assistant.scope" size="small" :aria-label="t('assistant.scope.title')" @change="pick">
      <t-option v-for="scope in ASSISTANT_SCOPES" :key="scope" :value="scope"
        :label="t(`assistant.scope.${scope}`)" :disabled="!allowed(scope)">
        <t-tooltip :content="t('assistant.scope.agentNoKB')" :disabled="allowed(scope)">
          <span>{{ t(`assistant.scope.${scope}`) }}</span>
        </t-tooltip>
      </t-option>
    </t-select>
    <span v-if="!assistant.contextAttached && assistant.scope !== 'workspace'" class="assistant-scope__hint">
      {{ t('assistant.scope.noContext') }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'
import { ASSISTANT_SCOPES, isAssistantScope, type AssistantScope } from '@/api/assistant'

const { t } = useI18n()
const assistant = useAssistantStore()
const allowed = (scope: AssistantScope) => scope !== 'knowledge_base' || !assistant.pageContext?.agentId
function pick(value: unknown) {
  if (isAssistantScope(value) && allowed(value)) assistant.setScope(value)
}
</script>

<style lang="less" scoped>
.assistant-scope {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 8px;
  min-width: 0;

  :deep(.t-select__wrap) {
    width: 160px;
  }
}

.assistant-scope__hint {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
