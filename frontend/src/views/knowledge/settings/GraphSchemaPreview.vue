<template>
  <t-dialog :visible="visible" :header="t('graphSettings.schema.preview')" :footer="false"
    width="min(960px, calc(100vw - 24px))" :close-on-overlay-click="false"
    @close="emit('update:visible', false)" @opened="mountGraph" @closed="destroyGraph">
    <div class="schema-graph-preview">
      <div class="preview-toolbar">
        <div class="preview-counts">
          <span>{{ schema.entity_types.length }} {{ t('graphSettings.schema.entityTypes') }}</span>
          <span>{{ schema.relation_types.length }} {{ t('graphSettings.schema.relationTypes') }}</span>
        </div>
        <div class="preview-tools">
          <t-tooltip v-for="tool in tools" :key="tool.icon" :content="t(tool.label)">
            <t-button shape="square" variant="text" :aria-label="t(tool.label)" :disabled="!ready" @click="tool.action">
              <template #icon><t-icon :name="tool.icon" /></template>
            </t-button>
          </t-tooltip>
        </div>
      </div>
      <div class="preview-stage">
        <div ref="container" class="schema-canvas" :aria-label="t('graphSettings.schema.preview')" />
        <div v-if="error || !schema.entity_types.length" class="preview-state" role="status">
          {{ error ? t(`graphSettings.schema.errors.${error}`) : t('graphSettings.schema.empty') }}
        </div>
        <div v-else-if="failed" class="preview-state" role="alert">
          {{ t('graphSettings.schema.renderFailed') }}
          <t-button variant="text" @click="mountGraph">{{ t('common.retry') }}</t-button>
        </div>
      </div>
      <div class="preview-inspector">
        <template v-if="selected">
          <span v-if="selected.color" class="type-swatch" :style="{ background: selected.color }" />
          <strong>{{ selected.name }}</strong>
          <span v-if="selected.sourceName" class="selected-triple">
            {{ selected.sourceName }} <t-icon name="arrow-right" /> {{ selected.targetName }}
          </span>
          <span v-if="selected.description" class="selected-description">{{ selected.description }}</span>
        </template>
        <div v-else class="preview-legend">
          <button v-for="(entity, index) in schema.entity_types" :key="index" class="legend-item"
            @click="selectEntity(index)">
            <span class="type-swatch" :style="{ background: schemaGraphColors[index % schemaGraphColors.length] }" />
            <span>{{ entity.name }}</span>
          </button>
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Core, Layouts } from 'cytoscape'
import { graphSchemaError, type GraphSchema } from '@/types/graphSchema'
import { schemaGraphColors, schemaGraphElements } from '@/types/schemaGraphPreview'

const props = defineProps<{ visible: boolean; schema: GraphSchema }>()
const emit = defineEmits<{ 'update:visible': [visible: boolean] }>()
const { t } = useI18n()
const container = ref<HTMLElement>()
const ready = ref(false)
const failed = ref(false)
const error = computed(() => graphSchemaError(props.schema))
const selected = ref<{ name: string; description?: string; color?: string; sourceName?: string; targetName?: string }>()
let cy: Core | undefined
let layout: Layouts | undefined
let resize: ResizeObserver | undefined
let generation = 0
const fit = () => {
  if (!cy) return
  cy.fit(cy.elements(), 56)
  cy.zoom(Math.min(cy.zoom(), 1.3))
  cy.center()
}
const zoom = (factor: number) => {
  if (!cy) return
  cy.zoom({ level: Math.max(cy.minZoom(), Math.min(cy.maxZoom(), cy.zoom() * factor)),
    renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } })
}
const relayout = () => {
  if (!cy) return
  layout?.stop()
  layout = cy.layout({
    name: 'cose', animate: true, animationDuration: 550, randomize: false,
    nodeRepulsion: () => 16000, idealEdgeLength: () => 150, edgeElasticity: () => 100,
    nodeOverlap: 24, componentSpacing: 100, padding: 56, fit: false,
  })
  layout.one('layoutstop', fit)
  layout.run()
}
const tools = [
  { icon: 'add', label: 'graphSettings.schema.zoomIn', action: () => zoom(1.25) },
  { icon: 'remove', label: 'graphSettings.schema.zoomOut', action: () => zoom(0.8) },
  { icon: 'fullscreen', label: 'graphSettings.schema.fit', action: fit },
  { icon: 'refresh', label: 'graphSettings.schema.layout', action: relayout },
]
const selectEntity = (index: number) => {
  if (!cy) return
  const entity = cy.getElementById(`entity-${index}`)
  cy.elements().unselect()
  entity.select()
  selected.value = entity.data()
  cy.animate({ center: { eles: entity }, duration: 220 })
}
function destroyGraph() {
  ++generation
  layout?.stop()
  resize?.disconnect()
  cy?.destroy()
  cy = undefined
  layout = undefined
  ready.value = false
  selected.value = undefined
}
async function mountGraph() {
  destroyGraph()
  failed.value = false
  const current = generation
  if (!props.visible || error.value || !props.schema.entity_types.length) return
  await nextTick()
  try {
    const { default: cytoscape } = await import('cytoscape')
    if (current !== generation || !props.visible || !container.value) return
    cy = cytoscape({
      container: container.value,
      elements: schemaGraphElements(props.schema),
      minZoom: 0.15, maxZoom: 3, wheelSensitivity: 0.18,
      layout: { name: 'preset' },
      style: [
        { selector: 'node', style: {
          'background-color': 'data(color)', 'border-width': 2, 'border-color': '#ffffff',
          width: 84, height: 84, label: 'data(name)', color: '#ffffff',
          'font-size': 12, 'font-weight': 600, 'text-valign': 'center', 'text-halign': 'center',
          'text-wrap': 'ellipsis', 'text-max-width': '72px', 'overlay-opacity': 0,
        } },
        { selector: 'edge', style: {
          width: 1.8, 'line-color': '#9aa5ae', 'target-arrow-color': '#9aa5ae',
          'target-arrow-shape': 'triangle', 'arrow-scale': 1.1, 'curve-style': 'bezier',
          'control-point-step-size': 64, label: 'data(name)', color: '#47545f',
          'font-size': 11, 'text-rotation': 'autorotate', 'text-background-color': '#ffffff',
          'text-background-opacity': 0.94, 'text-background-padding': '4px',
          'text-wrap': 'ellipsis', 'text-max-width': '160px', 'loop-direction': '-45deg', 'loop-sweep': '65deg',
        } },
        { selector: 'node:selected', style: { 'border-width': 5, 'border-color': '#252d35' } },
        { selector: 'edge:selected', style: {
          width: 3, 'line-color': '#252d35', 'target-arrow-color': '#252d35', color: '#252d35',
        } },
        { selector: 'node:active', style: { 'overlay-opacity': 0.08, 'overlay-padding': 8 } },
      ],
    })
    cy.on('tap', 'node, edge', event => { selected.value = event.target.data() })
    cy.on('tap', event => {
      if (event.target === cy) { selected.value = undefined; cy?.elements().unselect() }
    })
    resize = new ResizeObserver(() => { cy?.resize(); fit() })
    resize.observe(container.value)
    ready.value = true
    relayout()
  } catch {
    if (current === generation) { failed.value = true; destroyGraph() }
  }
}
watch(() => props.visible, visible => { if (!visible) destroyGraph() })
watch(() => props.schema, () => { if (props.visible) mountGraph() }, { deep: true })
onMounted(() => { if (props.visible) mountGraph() })
onBeforeUnmount(destroyGraph)
</script>

<style lang="less" scoped>
.schema-graph-preview { min-width: 0; color: var(--td-text-color-primary); }
.preview-toolbar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; padding-bottom: 12px; }
.preview-counts { display: flex; flex-wrap: wrap; gap: 16px; font-size: var(--app-text-sm); color: var(--td-text-color-secondary); }
.preview-tools { display: flex; gap: 2px; }
.preview-stage { position: relative; border-top: 1px solid var(--td-component-stroke); border-bottom: 1px solid var(--td-component-stroke); }
.schema-canvas { width: 100%; height: min(56vh, 520px); min-height: 260px; background: #fff; }
.preview-state { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 12px; padding: 24px; color: var(--td-text-color-secondary); text-align: center; }
.preview-inspector { min-height: 44px; max-height: 120px; overflow: auto; display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; padding-top: 12px; font-size: var(--app-text-md); overflow-wrap: anywhere; }
.preview-legend { display: flex; flex-wrap: wrap; gap: 8px 16px; width: 100%; }
.legend-item { display: inline-flex; align-items: center; gap: 6px; border: 0; padding: 4px 0; background: none; color: inherit; cursor: pointer; max-width: 100%; text-align: left; font: inherit; }
.legend-item:hover { color: var(--td-brand-color); }
.legend-item:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
.type-swatch { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
.selected-triple { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px; color: var(--td-text-color-secondary); }
.selected-description { flex-basis: 100%; color: var(--td-text-color-secondary); line-height: 1.5; }
@media (max-width: 600px) {
  .schema-canvas { height: 46vh; min-height: 240px; }
  .preview-tools :deep(.t-button) { width: 40px; height: 40px; }
}
</style>
