<template>
  <span v-if="value == null" class="empty-value">—</span>
  <template v-else-if="Array.isArray(value)">
    <span v-if="!value.length" class="empty-value">{{ $t('agentStream.assistantTools.empty') }}</span>
    <ol v-else class="value-list">
      <li v-for="(item, index) in value" :key="index"><OperationValue :value="item" /></li>
    </ol>
  </template>
  <template v-else-if="typeof value === 'object'">
    <span v-if="!Object.keys(value).length" class="empty-value">{{ $t('agentStream.assistantTools.empty') }}</span>
    <dl v-else class="value-fields">
      <div v-for="(item, key) in value" :key="key">
        <dt>{{ key }}</dt><dd><OperationValue :value="item" /></dd>
      </div>
    </dl>
  </template>
  <span v-else class="scalar-value">{{ value }}</span>
</template>

<script setup lang="ts">
defineProps<{ value: unknown }>()
</script>

<style scoped lang="less">
.empty-value { color: var(--td-text-color-placeholder); }
.scalar-value { white-space: pre-wrap; overflow-wrap: anywhere; }
.value-list { margin: 0; padding-left: 22px; }
.value-list > li + li { margin-top: 8px; padding-top: 8px; border-top: 1px solid var(--td-component-stroke); }
.value-fields { margin: 0; }
.value-fields > div { display: grid; grid-template-columns: minmax(80px, 30%) minmax(0, 1fr); gap: 10px; padding: 4px 0; }
dt { color: var(--td-text-color-secondary); overflow-wrap: anywhere; }
dd { margin: 0; min-width: 0; }
</style>
