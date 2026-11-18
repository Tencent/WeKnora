<template>
  <div v-if="source !== undefined" class="doc-md">
    <DocMarkdown v-for="(block, index) in blocks" :key="index" :token="block" />
  </div>
  <component
    v-else-if="token?.type === 'heading'"
    :is="headingTag"
    :class="`doc-h doc-h-${token.depth || 2}`"
  >
    <DocMarkdown v-for="(child, index) in inline(token)" :key="index" :token="child" />
  </component>
  <p v-else-if="token?.type === 'paragraph'">
    <DocMarkdown v-for="(child, index) in inline(token)" :key="index" :token="child" />
  </p>
  <blockquote v-else-if="token?.type === 'blockquote'">
    <DocMarkdown v-for="(child, index) in token.tokens || []" :key="index" :token="child" />
  </blockquote>
  <component
    :is="token?.ordered ? 'ol' : 'ul'"
    v-else-if="token?.type === 'list'"
  >
    <li v-for="(item, index) in token.items || []" :key="index">
      <DocMarkdown v-for="(child, childIndex) in item.tokens || []" :key="childIndex" :token="child" />
    </li>
  </component>
  <pre v-else-if="token?.type === 'code'" class="doc-code"><code>{{ token.text }}</code></pre>
  <table v-else-if="token?.type === 'table'">
    <thead>
      <tr>
        <th v-for="(cell, index) in token.header || []" :key="index">
          <DocMarkdown v-for="(child, childIndex) in inline(cell)" :key="childIndex" :token="child" />
        </th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="(row, index) in token.rows || []" :key="index">
        <td v-for="(cell, cellIndex) in row" :key="cellIndex">
          <DocMarkdown v-for="(child, childIndex) in inline(cell)" :key="childIndex" :token="child" />
        </td>
      </tr>
    </tbody>
  </table>
  <hr v-else-if="token?.type === 'hr'" />
  <a
    v-else-if="token?.type === 'link' && linkHref"
    :href="linkHref"
    target="_blank"
    rel="noopener noreferrer"
  >
    <DocMarkdown v-for="(child, index) in inline(token)" :key="index" :token="child" />
  </a>
  <span v-else-if="token?.type === 'link'">{{ token.text }}</span>
  <strong v-else-if="token?.type === 'strong'">
    <DocMarkdown v-for="(child, index) in inline(token)" :key="index" :token="child" />
  </strong>
  <em v-else-if="token?.type === 'em'">
    <DocMarkdown v-for="(child, index) in inline(token)" :key="index" :token="child" />
  </em>
  <code v-else-if="token?.type === 'codespan'">{{ token.text }}</code>
  <template v-else-if="token?.type === 'text' && token.tokens?.length">
    <DocMarkdown v-for="(child, index) in token.tokens" :key="index" :token="child" />
  </template>
  <template v-else-if="token?.type === 'text' || token?.type === 'escape' || token?.type === 'html' || token?.type === 'image'">{{ token.text }}</template>
  <template v-else-if="token?.type === 'space' || token?.type === 'br'" />
  <template v-else>{{ token?.text || '' }}</template>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { marked } from 'marked'
import { safeDocUrl } from '@/utils/catalogToolDisplay'

type MdToken = {
  type: string
  text?: string
  depth?: number
  href?: string
  ordered?: boolean
  tokens?: MdToken[]
  items?: MdToken[]
  header?: MdToken[]
  rows?: MdToken[][]
}

const props = defineProps<{
  source?: string
  token?: MdToken
}>()

const blocks = computed(() => (
  props.source != null ? marked.lexer(props.source) as MdToken[] : []
))
const headingTag = computed(() => {
  const depth = Math.min(6, Math.max(1, props.token?.depth || 2))
  return `h${depth}`
})
const linkHref = computed(() => safeDocUrl(props.token?.href || ''))

function inline(token: MdToken): MdToken[] {
  if (token.tokens?.length) return token.tokens
  if (token.text) return [{ type: 'text', text: token.text }]
  return []
}
</script>

<style lang="less" scoped>
.doc-md {
  max-height: 360px;
  overflow: auto;
  font-size: var(--app-text-sm);
  line-height: 1.6;
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
}
.doc-md :deep(p) { margin: 0 0 8px; }
.doc-md :deep(ul), .doc-md :deep(ol) { margin: 0 0 8px; padding-left: 1.2em; }
.doc-md :deep(li + li) { margin-top: 2px; }
.doc-md :deep(.doc-h) { margin: 10px 0 6px; font-weight: 600; line-height: 1.4; }
.doc-md :deep(.doc-h-1), .doc-md :deep(.doc-h-2) { font-size: var(--app-text-md); }
.doc-md :deep(a) { color: var(--td-brand-color); }
.doc-md :deep(code) {
  padding: 0 4px;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-secondarycontainer);
  font-family: var(--app-font-family-mono, monospace);
  font-size: var(--app-text-sm);
}
.doc-md :deep(.doc-code) {
  margin: 0 0 8px;
  padding: 8px 10px;
  max-height: 220px;
  overflow: auto;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--app-radius-sm);
  font: var(--app-text-sm)/1.55 var(--app-font-family-mono, monospace);
  white-space: pre-wrap;
}
.doc-md :deep(.doc-code code) { padding: 0; background: transparent; }
.doc-md :deep(blockquote) {
  margin: 0 0 8px;
  padding-left: 10px;
  border-left: 2px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
}
.doc-md :deep(table) {
  width: 100%;
  margin: 0 0 8px;
  border-collapse: collapse;
  font-size: var(--app-text-xs);
}
.doc-md :deep(th), .doc-md :deep(td) {
  padding: 4px 8px;
  border: 1px solid var(--td-component-stroke);
  text-align: left;
  vertical-align: top;
}
.doc-md :deep(hr) { border: 0; border-top: 1px solid var(--td-component-stroke); margin: 8px 0; }
</style>
