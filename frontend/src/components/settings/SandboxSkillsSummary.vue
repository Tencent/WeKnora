<template>
  <div class="sandbox-skills-summary" @click.stop @keydown.enter.stop @keydown.space.stop>
    <t-popup v-model:visible="open" trigger="click" placement="bottom-left" attach="body" destroy-on-close
      overlay-class-name="sandbox-skill-menu-overlay" :overlay-inner-style="{ padding: 0 }">
      <button type="button" class="skills-entry" :aria-expanded="open" :aria-label="$t('skillDiscovery.cardExpand')">
        <t-icon name="tools" size="13px" class="skills-entry__icon" />
        <span>{{ $t('skillDiscovery.cardBuiltins') }} <b>{{ builtinCount }}</b></span>
        <span class="skills-entry__sep">·</span>
        <span>{{ $t('skillDiscovery.cardInstalled') }} <b>{{ installedCount }}</b></span>
        <t-icon v-if="liveInstalled.some(skill => skill.status === 'failed')" name="error-circle" size="13px" class="skills-entry__error" />
        <t-icon name="chevron-down" size="13px" class="skills-entry__arrow" />
      </button>
      <template #content>
        <div class="sandbox-skill-menu" @click.stop @keydown.enter.stop @keydown.space.stop>
          <div class="skill-menu__heading">
            <span>{{ $t('skillDiscovery.cardBuiltins') }}</span>
            <span v-if="builtin?.known" class="skill-menu__version">{{ builtin.version }}</span>
          </div>
          <p v-if="builtin === undefined" class="skill-menu__hint">{{ $t('skillDiscovery.checkingPreinstalled') }}</p>
          <p v-else-if="!builtin?.known" class="skill-menu__hint" :title="$t('skillDiscovery.preinstalledUnknown')">{{ $t('skillDiscovery.cardUnknown') }}</p>
          <template v-else>
            <div v-for="skill in builtin.skills" :key="skill.name" class="skill-menu__row" :title="skill.description">
              <t-icon name="layers" size="14px" class="skill-menu__icon" />
              <span class="skill-menu__name">{{ skill.name }}</span>
              <t-icon name="check" size="14px" class="skill-menu__ready" :aria-label="$t('settings.sandbox.skillStatusReady')" />
            </div>
            <p v-if="!builtin.skills.length && !builtin.unavailable" class="skill-menu__hint">{{ $t('skillDiscovery.cardNone') }}</p>
            <p v-if="builtin.unavailable" class="skill-menu__hint">{{ $t('skillDiscovery.cardIncompatible', { count: builtin.unavailable }) }}</p>
          </template>
          <div class="skill-menu__split" />
          <div class="skill-menu__heading">{{ $t('skillDiscovery.cardInstalled') }}</div>
          <p v-if="installed === undefined" class="skill-menu__hint">{{ $t('common.loading') }}</p>
          <p v-else-if="installed === null" class="skill-menu__hint">{{ $t('skillDiscovery.cardLoadFailed') }}</p>
          <p v-else-if="!liveInstalled.length" class="skill-menu__hint">{{ $t('skillDiscovery.cardNone') }}</p>
          <div v-for="skill in liveInstalled" :key="skill.id" class="skill-menu__row" :title="[skill.name, skill.version, statusText(skill)].filter(Boolean).join(' · ')">
            <t-icon name="tools" size="14px" class="skill-menu__icon skill-menu__icon--installed" />
            <span class="skill-menu__name">{{ skill.name }}</span>
            <span class="skill-menu__status" :class="{ 'skill-menu__status--error': skill.status === 'failed', 'skill-menu__status--busy': ['installing', 'removing'].includes(skill.status) }">{{ statusText(skill) }}</span>
          </div>
        </div>
      </template>
    </t-popup>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BuiltinSkillsSummary } from '@/api/skill'
import type { ConfigSkill } from '@/api/system'

const props = defineProps<{
  builtin?: BuiltinSkillsSummary | null
  installed?: ConfigSkill[] | null
  usableInstalledNames?: string[]
}>()
const { t } = useI18n()
const builtinCount = computed(() => props.builtin === undefined ? '…' : props.builtin?.known ? props.builtin.skills.length : '?')
const installedCount = computed(() => props.installed === undefined ? '…' : props.installed === null ? '?' : liveInstalled.value.length)
const open = ref(false)
const statusPriority = (skill: ConfigSkill) => skill.status === 'failed' ? 0 : ['installing', 'removing'].includes(skill.status) ? 1 : !skill.enabled ? 2 : 3
const liveInstalled = computed(() => (props.installed || []).filter(skill => skill.status !== 'removed').sort((a, b) => statusPriority(a) - statusPriority(b)))

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
.sandbox-skills-summary { margin-top: 10px; }
.skills-entry {
  display: inline-flex; align-items: center; gap: 5px; max-width: 100%; padding: 3px 7px;
  border: 0; border-radius: 6px; background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary); font: inherit; font-size: 12px; line-height: 18px; cursor: pointer;
  b { font-weight: 500; color: var(--td-text-color-primary); }
  &:hover, &[aria-expanded='true'] { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
  &:focus-visible { outline: 2px solid #7486d8; outline-offset: 2px; }
  &__icon { color: #8192d5; }
  &__sep, &__arrow { color: var(--td-text-color-placeholder); }
  &__error { color: var(--td-error-color); }
}
.sandbox-skill-menu { width: 300px; max-width: calc(100vw - 32px); max-height: min(440px, 70vh); overflow-y: auto; padding: 4px 0; }
.skill-menu {
  &__heading { display: flex; align-items: center; gap: 8px; padding: 6px 12px 4px; font-size: 12px; line-height: 20px; color: var(--td-text-color-placeholder); }
  &__version { margin-left: auto; font-size: 11px; }
  &__row { display: flex; align-items: center; gap: 8px; min-height: 32px; padding: 0 12px; font-size: 13px; color: var(--td-text-color-primary); &:hover { background: var(--td-bg-color-container-hover); } }
  &__icon { flex-shrink: 0; color: #8192d5; &--installed { color: #a28ac2; } }
  &__name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  &__ready { color: var(--td-text-color-placeholder); }
  &__status { flex-shrink: 0; font-size: 11px; color: var(--td-text-color-placeholder); &--error { color: var(--td-error-color); } &--busy { color: var(--td-warning-color); } }
  &__hint { margin: 0; padding: 4px 12px 8px; font-size: 12px; line-height: 20px; color: var(--td-text-color-placeholder); }
  &__split { margin: 4px 0; height: 1px; background: var(--td-component-stroke); }
}
:global(.sandbox-skill-menu-overlay .t-popup__content) { border: 1px solid var(--td-component-stroke); border-radius: 8px; overflow: hidden; }
</style>
