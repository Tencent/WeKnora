<template>
  <article :id="`knowledge-anchor-${anchor.id}`" class="knowledge-anchor" :style="{ '--knowledge-color': typeStyle.colorVar }">
    <header>
      <div class="knowledge-anchor__eyebrow">
        <span class="knowledge-anchor__type"><t-icon :name="typeStyle.icon" />{{ typeStyle.label }}</span>
        <button class="knowledge-anchor__time" type="button" title="定位到当前视频此位置" @click="emit('seek', anchor.seconds)">{{ anchor.timestamp }}</button>
      </div>
      <h3>{{ anchor.content }}</h3>
    </header>

    <p v-if="anchor.coreContent" class="knowledge-anchor__core">{{ anchor.coreContent }}</p>

    <div class="knowledge-anchor__actions">
      <span>{{ anchor.relations?.length || 0 }} 条知识关系 · {{ anchor.evidence?.length || 0 }} 条证据</span>
      <button type="button" class="knowledge-anchor__toggle" :aria-expanded="expanded" @click="emit('toggle')">
        {{ expanded ? '收起' : '阅读全文' }}
        <t-icon name="chevron-down" :class="{ 'is-expanded': expanded }" />
      </button>
    </div>

    <div v-if="expanded" class="knowledge-anchor__reader">
      <section v-if="anchor.structureFields?.length" class="knowledge-anchor__section" aria-label="正文">
        <div v-for="field in anchor.structureFields" :key="field.key" class="knowledge-anchor__paragraph">
          <h4>{{ field.label }}</h4>
          <p>{{ field.value }}</p>
        </div>
      </section>

      <section v-if="anchor.relations?.length" class="knowledge-anchor__section">
        <h4>知识关系</h4>
        <ul class="knowledge-anchor__relations">
          <li v-for="relation in anchor.relations" :key="relation.id">
            <button type="button" @click="emit('selectKnowledge', relation.targetId)">{{ relation.targetTitle }}</button>
            <span>{{ getRelationTypeLabel(relation.relationType) }}<template v-if="relation.confidence"> · {{ Math.round(relation.confidence * 100) }}%</template></span>
          </li>
        </ul>
      </section>

      <section v-if="anchor.evidence?.length" class="knowledge-anchor__section">
        <h4>原文证据</h4>
        <blockquote v-for="item in anchor.evidence" :key="item.id">
          <p>“{{ item.text }}”</p>
          <button type="button" class="knowledge-anchor__time" @click="emit('seek', item.seconds)">{{ item.timestamp }}</button>
        </blockquote>
      </section>

      <footer>
        <span>{{ anchor.sourceVideoTitle || '未命名视频' }}</span>
        <span>{{ anchor.timeRange || anchor.timestamp }}</span>
      </footer>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getRelationTypeLabel, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { CurrentKnowledgeAnchor } from '@/types/videohub'

const props = defineProps<{ anchor: CurrentKnowledgeAnchor; expanded?: boolean }>()
const emit = defineEmits<{ seek: [seconds: number]; toggle: []; selectKnowledge: [anchorId: string] }>()
const typeStyle = computed(() => KNOWLEDGE_TYPE_STYLES[props.anchor.knowledge_type])
</script>

<style scoped>
.knowledge-anchor { padding: 22px 4px 24px; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 68%, transparent); scroll-margin-top: 16px; }
.knowledge-anchor:first-child { padding-top: 6px; }
.knowledge-anchor header { max-width: 72ch; }
.knowledge-anchor__eyebrow { display: flex; align-items: center; justify-content: space-between; gap: var(--td-comp-margin-s); margin-bottom: 8px; }
.knowledge-anchor__type { display: inline-flex; align-items: center; gap: 5px; color: var(--knowledge-color); font-size: var(--td-font-size-body-small); font-weight: 500; }
.knowledge-anchor h3 { margin: 0; color: var(--td-text-color-primary); font-size: 18px; font-weight: 600; line-height: 1.45; overflow-wrap: anywhere; }
.knowledge-anchor__core { max-width: 72ch; margin: 10px 0 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: 1.75; }
.knowledge-anchor__actions { display: flex; max-width: 72ch; align-items: center; justify-content: space-between; gap: var(--td-comp-margin-s); margin-top: 12px; color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); }
.knowledge-anchor__toggle { display: inline-flex; align-items: center; gap: 4px; padding: 3px 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; cursor: pointer; }
.knowledge-anchor__toggle .t-icon { transition: transform .16s ease; }
.knowledge-anchor__toggle .t-icon.is-expanded { transform: rotate(180deg); }
.knowledge-anchor__reader { max-width: 72ch; margin-top: 18px; padding-top: 2px; }
.knowledge-anchor__section { padding: 18px 0; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 60%, transparent); }
.knowledge-anchor__paragraph + .knowledge-anchor__paragraph { margin-top: 18px; }
.knowledge-anchor__section h4, .knowledge-anchor__paragraph h4 { margin: 0 0 7px; color: var(--td-text-color-primary); font-size: var(--td-font-size-body-medium); font-weight: 600; }
.knowledge-anchor__paragraph p { margin: 0; color: var(--td-text-color-secondary); line-height: 1.75; white-space: pre-line; overflow-wrap: anywhere; }
.knowledge-anchor__relations { display: grid; gap: 10px; margin: 0; padding: 0; list-style: none; }
.knowledge-anchor__relations li { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.knowledge-anchor__relations button { min-width: 0; padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font: inherit; text-align: left; cursor: pointer; overflow-wrap: anywhere; }
.knowledge-anchor__relations span { flex: none; color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); }
.knowledge-anchor blockquote { margin: 0; padding: 0 0 0 14px; border-left: 2px solid color-mix(in srgb, var(--td-brand-color) 42%, transparent); }
.knowledge-anchor blockquote + blockquote { margin-top: 16px; }
.knowledge-anchor blockquote p { margin: 0 0 7px; color: var(--td-text-color-secondary); line-height: 1.75; }
.knowledge-anchor__time { flex: none; padding: 0; border: 0; background: transparent; color: var(--td-brand-color); font-family: var(--app-font-family-mono, monospace); font-size: var(--td-font-size-body-small); cursor: pointer; }
.knowledge-anchor__time:hover, .knowledge-anchor__relations button:hover, .knowledge-anchor__toggle:hover { text-decoration: underline; }
.knowledge-anchor footer { display: flex; justify-content: space-between; gap: 12px; padding-top: 14px; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 60%, transparent); color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); }
@media (max-width: 560px) { .knowledge-anchor { padding: 18px 2px; }.knowledge-anchor h3 { font-size: 16px; }.knowledge-anchor__relations li, .knowledge-anchor footer { align-items: flex-start; flex-direction: column; gap: 4px; } }
</style>
