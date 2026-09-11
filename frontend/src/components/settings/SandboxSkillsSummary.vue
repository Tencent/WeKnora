<template>
  <div class="sandbox-skills-summary" @click.stop @keydown.enter.stop @keydown.space.stop @keydown.esc.stop="close">
    <t-popup :visible="expanded" trigger="click" placement="bottom-left" attach="body" destroy-on-close
      overlay-class-name="sandbox-skills-overlay" :overlay-inner-style="{ padding: 0 }"
      @visible-change="emit('update:expanded', $event)">
      <button ref="trigger" type="button" class="skills-entry" :aria-expanded="expanded" :aria-controls="panelId"
        :aria-label="$t(expanded ? 'skillDiscovery.cardCollapse' : 'skillDiscovery.cardExpand')">
        <span class="skills-entry__group" :title="builtin?.known ? undefined : $t('skillDiscovery.preinstalledUnknown')">
          <template v-if="builtin?.known">{{ $t('skillDiscovery.cardBuiltins') }} <b>{{ builtin.skills.length }}</b></template>
          <span v-else class="skills-entry__muted">{{ $t('skillDiscovery.cardBuiltins') }} <b>{{ builtin === undefined ? '…' : '—' }}</b></span>
        </span>
        <span class="skills-entry__sep" aria-hidden="true" />
        <span class="skills-entry__group" :title="installed === null ? $t('skillDiscovery.cardLoadFailed') : undefined">
          {{ $t('skillDiscovery.cardInstalled') }} <b>{{ installedCount }}</b>
          <span v-if="hasFailure" class="skills-entry__error" :aria-label="$t('settings.sandbox.skillStatusFailed')" />
        </span>
        <t-icon name="chevron-down" size="14px" class="skills-entry__arrow" />
      </button>
      <template #content>
        <section @click.stop @keydown.esc.stop="close" :id="panelId" class="skill-panel" :aria-label="$t('skillDiscovery.cardExpand')">
          <div class="skill-panel__header">
            <t-tabs v-model="activeTab" class="skill-panel__tabs">
              <t-tab-panel value="builtin" :label="`${$t('skillDiscovery.cardBuiltins')}${builtin?.known ? ` (${builtin.skills.length})` : ''}`" />
              <t-tab-panel value="installed" :label="`${$t('skillDiscovery.cardInstalled')} (${installedCount})`" />
            </t-tabs>
            <span v-if="activeTab === 'builtin' && builtin?.known && builtin.version" class="skill-panel__version">v{{ builtin.version }}</span>
            <button type="button" class="skill-panel__close" :aria-label="$t('skillDiscovery.cardCollapse')" @click="close">
              <t-icon name="close" size="16px" />
            </button>
          </div>
          <div class="skill-panel__body">
            <template v-if="activeTab === 'builtin'">
              <p v-if="builtin === undefined" class="skill-panel__empty">{{ $t('skillDiscovery.checkingPreinstalled') }}</p>
              <div v-else-if="!builtin?.known" class="skill-panel__notice">
                <t-icon name="info-circle" size="14px" />
                <div><strong>{{ $t('skillDiscovery.cardUnknown') }}</strong><p>{{ $t('skillDiscovery.cardUnknownHint') }}</p></div>
              </div>
              <template v-else>
                <p v-if="builtin.skills.length" class="skill-panel__hint">{{ $t('skillDiscovery.alreadyPreinstalled') }}</p>
                <div class="skill-panel__grid">
                  <article v-for="skill in builtin.skills" :key="skill.name" class="skill-item">
                    <span class="skill-item__icon" :data-tone="presentation(skill.name).tone">
                      <t-icon :name="presentation(skill.name).icon" size="14px" />
                    </span>
                    <div class="skill-item__body">
                      <h4 :title="`${skill.name} — ${presentation(skill.name).description || skill.description || ''}`">{{ presentation(skill.name).title }}</h4>
                    </div>
                  </article>
                </div>
                <p v-if="!builtin.skills.length && !builtin.unavailable" class="skill-panel__empty">{{ $t('skillDiscovery.cardNone') }}</p>
                <p v-if="builtin.unavailable" class="skill-panel__notice">{{ $t('skillDiscovery.cardIncompatible', { count: builtin.unavailable }) }}</p>
              </template>
            </template>
            <template v-else>
              <p v-if="installed === undefined" class="skill-panel__empty">{{ $t('common.loading') }}</p>
              <p v-else-if="installed === null" class="skill-panel__notice">{{ $t('skillDiscovery.cardLoadFailed') }}</p>
              <p v-else-if="!liveInstalled.length" class="skill-panel__empty">{{ $t('skillDiscovery.cardNone') }}</p>
              <div v-else class="skill-panel__grid">
                <article v-for="skill in liveInstalled" :key="skill.id" class="skill-item">
                  <span class="skill-item__icon"><t-icon name="tools" size="14px" /></span>
                  <div class="skill-item__body">
                    <div class="skill-item__heading">
                      <h4 :title="`${skill.name} — ${presentation(skill.name).description || skill.description || ''}`">{{ skill.name }}</h4>
                      <span class="skill-item__status" :class="{ 'is-error': skill.status === 'failed', 'is-busy': ['installing', 'removing'].includes(skill.status) }">{{ statusText(skill) }}</span>
                    </div>
                    <p v-if="skill.status === 'failed' && skill.error" class="skill-item__error">{{ skill.error }}</p>
                  </div>
                </article>
              </div>
            </template>
          </div>
        </section>
      </template>
    </t-popup>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BuiltinSkillsSummary, DiscoverySkill } from '@/api/skill'
import type { ConfigSkill } from '@/api/system'
import { skillIcon, skillTone, localizedSkillText } from '@/utils/skillPresentation'

const props = defineProps<{
  expanded: boolean
  catalog: DiscoverySkill[]
  builtin?: BuiltinSkillsSummary | null
  installed?: ConfigSkill[] | null
  usableInstalledNames?: string[]
}>()
const emit = defineEmits<{ 'update:expanded': [value: boolean] }>()
const { t, locale } = useI18n()
const panelId = useId()
const trigger = ref<HTMLButtonElement | null>(null)
const activeTab = ref('builtin')
const statusPriority = (skill: ConfigSkill) => skill.status === 'failed' ? 0 : ['installing', 'removing'].includes(skill.status) ? 1 : !skill.enabled ? 2 : 3
const liveInstalled = computed(() => (props.installed || []).filter(skill => skill.status !== 'removed').sort((a, b) => statusPriority(a) - statusPriority(b)))
const installedCount = computed(() => props.installed === undefined ? '…' : props.installed === null ? '—' : liveInstalled.value.length)
const hasFailure = computed(() => liveInstalled.value.some(skill => skill.status === 'failed'))
const presentations = computed(() => new Map(props.catalog.map(item => [item.name, {
  title: localizedSkillText(item.title, locale.value) || item.name,
  description: localizedSkillText(item.description, locale.value),
  icon: skillIcon(item), tone: skillTone(item),
}])))
const presentation = (name: string) => presentations.value.get(name) || { title: name, description: '', icon: 'file', tone: 'blue' }
watch(() => props.expanded, expanded => {
  if (expanded) activeTab.value = hasFailure.value || (!props.builtin?.skills.length && liveInstalled.value.length) ? 'installed' : 'builtin'
})
function close() {
  if (!props.expanded) return
  emit('update:expanded', false)
  void nextTick(() => trigger.value?.focus())
}
function statusText(skill: ConfigSkill): string {
  if (skill.status === 'installing') return t('settings.sandbox.skillStatusInstalling')
  if (skill.status === 'failed') return t('settings.sandbox.skillStatusFailed')
  if (skill.status === 'removing') return t('settings.sandbox.skillStatusRemoving')
  if (skill.status === 'ready') {
    if (!skill.enabled) return t('skillDiscovery.cardDisabled')
    if (!props.usableInstalledNames) return t('skillDiscovery.cardAvailabilityUnknown')
    if (!props.usableInstalledNames.includes(skill.name)) return t('skillDiscovery.cardNotInImage')
    return t('settings.sandbox.skillStatusReady')
  }
  return t('skillDiscovery.cardAvailabilityUnknown')
}
</script>

<style scoped lang="less">
.sandbox-skills-summary { min-width: 0; max-width: 100%; }
.skills-entry {
  display: inline-flex; align-items: center; gap: 6px; max-width: 100%; padding: 2px 6px;
  border: 0; border-radius: 6px; background: transparent; text-align: left;
  color: var(--td-text-color-secondary); font: inherit; font-size: 12px; line-height: 18px; cursor: pointer;
  b { font-weight: 500; color: var(--td-text-color-primary); font-variant-numeric: tabular-nums; }
  &:hover, &[aria-expanded='true'] { background: var(--td-bg-color-secondarycontainer); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
  &__group { display: inline-flex; align-items: baseline; gap: 5px; white-space: nowrap; }
  &__muted, &__arrow { color: var(--td-text-color-placeholder); }
  &__sep { width: 1px; height: 12px; background: var(--td-component-stroke); flex-shrink: 0; }
  &__arrow { transition: transform 0.16s ease; }
  &[aria-expanded='true'] &__arrow { transform: rotate(180deg); }
  &__error { width: 6px; height: 6px; border-radius: 50%; background: var(--td-error-color); }
}
.skill-panel {
  cursor: default; width: 360px; max-width: calc(100vw - 32px); min-width: 0;
  border-radius: 8px;
  background: var(--td-bg-color-container); overflow: hidden;
  animation: skill-panel-enter 0.16s ease-out;
  &__header { display: flex; align-items: center; gap: 6px; padding: 0 8px; border-bottom: 1px solid var(--td-component-stroke); }
  &__tabs { flex: 1; min-width: 0;
    :deep(.t-tabs__content) { display: none; }
    :deep(.t-tabs__nav-item) { font-size: 12px; }
    :deep(.t-tabs__nav-item-wrapper) { padding: 0 10px; margin: 0; }
  }
  &__version { font-size: 11px; color: var(--td-text-color-placeholder); }
  &__close { display: flex; align-items: center; justify-content: center; flex-shrink: 0; width: 26px; height: 26px; border: 0; border-radius: 4px; background: transparent; color: var(--td-text-color-secondary); cursor: pointer;
    &:hover { background: var(--td-bg-color-secondarycontainer); }
    &:focus-visible { outline: 2px solid var(--td-brand-color); }
  }
  &__body { padding: 8px; max-height: min(300px, 50vh); overflow-y: auto; overscroll-behavior: contain; }
  &__grid { display: flex; flex-direction: column; gap: 2px; }
  &__hint { margin: 4px 8px 8px; font-size: 12px; color: var(--td-text-color-secondary); }
  &__empty { margin: 0; padding: 24px 12px; text-align: center; font-size: 13px; color: var(--td-text-color-placeholder); }
  &__notice { display: flex; align-items: flex-start; gap: 10px; margin: 0; padding: 14px; border-radius: 6px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.6;
    strong { font-weight: 500; color: var(--td-text-color-primary); }
    p { margin: 4px 0 0; }
    .t-icon { flex-shrink: 0; margin-top: 2px; }
  }
}
.skill-item {
  display: flex; align-items: center; gap: 8px; min-width: 0; padding: 6px 8px;
  border-radius: 6px;
  &__icon { display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 20px; height: 20px; border-radius: 7px; color: var(--td-brand-color); background: var(--td-brand-color-light);
    &[data-tone='violet'] { color: #7857ad; background: #7857ad12; }
    &[data-tone='green'] { color: #288763; background: #28876312; }
    &[data-tone='rose'] { color: #b05f73; background: #b05f7312; }
    &[data-tone='amber'] { color: #a77c28; background: #a77c2812; }
    &[data-tone='cyan'] { color: #27858d; background: #27858d12; }
  }
  &__body { flex: 1; min-width: 0; }
  &__heading { display: flex; align-items: center; gap: 8px; }
  h4 { margin: 0; font-size: 13px; font-weight: 500; color: var(--td-text-color-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  &__status { font-size: 11px; color: var(--td-text-color-placeholder); }
  &__status { flex-shrink: 0; margin-left: auto; &.is-error { color: var(--td-error-color); } &.is-busy { color: var(--td-warning-color); } }
  &__error { margin: 6px 0 0; color: var(--td-error-color); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
}
@keyframes skill-panel-enter { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) { .skills-entry__arrow { transition: none; } .skill-panel { animation: none; } }
</style>

<style lang="less">
.sandbox-skills-overlay .t-popup__content { border-radius: 10px; overflow: hidden; }
</style>
