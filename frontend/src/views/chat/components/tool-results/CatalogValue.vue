<template>
  <pre v-if="nestedJson || longText" class="cv-json">{{ nestedJson || longText }}</pre>
  <span v-else-if="scalar" class="cv-scalar">{{ scalar }}</span>
  <ul v-else-if="primitives" class="cv-primitives">
    <li v-for="(item, index) in primitives" :key="index">{{ item }}</li>
  </ul>
  <div v-else-if="cards" class="cv-cards">
    <article v-for="card in cards" :key="card.key" class="cv-card">
      <div class="cv-card__title">
        <span class="cv-index">#{{ card.index }}</span>
        <span>{{ card.title }}</span>
      </div>
      <dl v-if="card.fields.length" class="cv-object">
        <div v-for="field in card.fields" :key="field.key" class="cv-row">
          <dt>{{ field.key }}</dt>
          <dd><CatalogValue :value="field.value" :depth="depth + 1" /></dd>
        </div>
      </dl>
    </article>
  </div>
  <div v-else-if="items" class="cv-list">
    <div v-for="(item, index) in items" :key="index" class="cv-list__item">
      <CatalogValue :value="item" :depth="depth + 1" />
    </div>
  </div>
  <dl v-else-if="fields" class="cv-object">
    <div v-for="field in fields" :key="field.key" class="cv-row">
      <dt>{{ field.key }}</dt>
      <dd><CatalogValue :value="field.value" :depth="depth + 1" /></dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const TITLE_KEYS = ['title', 'name', 'question', 'summary', 'slug', 'path', 'id']

const props = withDefaults(defineProps<{
  value: unknown
  depth?: number
}>(), { depth: 0 })

const depth = computed(() => props.depth ?? 0)

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function formatScalar(value: unknown): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

function isPrimitive(value: unknown): boolean {
  return value == null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean'
}

function isLongText(value: string): boolean {
  return value.includes('\n') || value.length > 180
}

const items = computed(() => Array.isArray(props.value) ? props.value : null)
const recordValue = computed(() => isRecord(props.value) ? props.value : null)

const nestedJson = computed(() => {
  if (depth.value < 4 || (items.value == null && recordValue.value == null)) return ''
  try {
    return JSON.stringify(props.value, null, 2)
  } catch {
    return String(props.value)
  }
})

const longText = computed(() => (
  typeof props.value === 'string' && isLongText(props.value) ? props.value : ''
))

const scalar = computed(() => {
  if (nestedJson.value || longText.value || items.value || recordValue.value) return ''
  return formatScalar(props.value)
})

const primitives = computed(() => {
  if (!items.value || items.value.some((item) => !isPrimitive(item))) return null
  return items.value.map((item) => formatScalar(item))
})

const cards = computed(() => {
  if (!items.value || !items.value.length || items.value.some((item) => !isRecord(item))) return null
  return items.value.map((item, index) => {
    const row = item as Record<string, unknown>
    const titleKey = TITLE_KEYS.find((key) => typeof row[key] === 'string' && String(row[key]).trim()) || ''
    const title = titleKey ? String(row[titleKey]).trim() : `#${index + 1}`
    const fields = Object.entries(row)
      .filter(([key, value]) => key !== titleKey && !isEmpty(value))
      .map(([key, value]) => ({ key, value }))
    return { key: `${title}-${index}`, index: index + 1, title, fields }
  })
})

const fields = computed(() => {
  if (!recordValue.value) return null
  return Object.entries(recordValue.value)
    .filter(([, value]) => !isEmpty(value))
    .map(([key, value]) => ({ key, value }))
})

function isEmpty(value: unknown): boolean {
  if (value == null) return true
  if (typeof value === 'string') return value.trim() === ''
  if (Array.isArray(value)) return value.length === 0
  if (isRecord(value)) return Object.keys(value).length === 0
  return false
}
</script>

<style lang="less" scoped>
.cv-scalar, .cv-primitives li { white-space: pre-wrap; overflow-wrap: anywhere; }
.cv-json {
  margin: 0;
  padding: 8px 10px;
  max-height: 240px;
  overflow: auto;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-sm);
  font: var(--app-text-sm)/1.55 var(--app-font-family-mono, monospace);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.cv-primitives, .cv-list { margin: 0; padding: 0; list-style: none; }
.cv-primitives li + li, .cv-list__item + .cv-list__item { margin-top: 4px; }
.cv-cards { display: flex; flex-direction: column; gap: 8px; }
.cv-card {
  padding: 8px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
}
.cv-card__title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.cv-index {
  flex-shrink: 0;
  font-size: var(--app-text-xs);
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}
.cv-object { margin: 6px 0 0; }
.cv-row {
  display: grid;
  grid-template-columns: minmax(88px, 28%) minmax(0, 1fr);
  gap: 8px;
  padding: 4px 0;
}
.cv-row + .cv-row { border-top: 1px solid var(--td-component-stroke); }
.cv-row dt {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font: var(--app-text-sm)/1.5 var(--app-font-family-mono, monospace);
  overflow-wrap: anywhere;
}
.cv-row dd { margin: 0; min-width: 0; }
</style>
