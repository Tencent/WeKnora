<template>
  <t-popup v-model:visible="open" trigger="click" placement="top-left" :disabled="saving"
    :overlay-inner-style="{ padding: '4px', borderRadius: 'var(--app-radius-lg)' }">
    <t-tooltip :content="hint" placement="top" :popup-props="{ overlayClassName: 'input-field-tooltip' }">
      <button type="button" class="host-approval-mode" :disabled="saving" aria-haspopup="menu"
        :aria-expanded="open" :aria-label="label">
        <t-icon :name="iconName(mode)" size="14px" class="host-approval-mode__icon" />
        <span class="host-approval-mode__name">{{ label }}</span>
        <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" class="host-approval-mode__arrow"
          :class="{ rotate: open }" aria-hidden="true">
          <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
        </svg>
      </button>
    </t-tooltip>
    <template #content>
      <div class="host-approval-mode__menu" role="menu" :aria-label="label">
        <button v-for="m in HOST_APPROVAL_MODES" :key="m" type="button" role="menuitemradio" class="host-approval-mode__option"
          :class="{ selected: m === mode }" :aria-checked="m === mode" :disabled="saving" @click="choose(m)">
          <span class="host-approval-mode__option-main">
            <t-icon :name="iconName(m)" size="14px" />
            <span>{{ $t(`input.hostApprovalMode.${m}`) }}</span>
          </span>
          <t-icon v-if="m === mode" name="check" size="14px" />
        </button>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getHostApprovalMode, setHostApprovalMode } from '@/api/chat/index'
import { HOST_APPROVAL_MODES, modeAfterLoad, parseHostApprovalMode, readModeResponse, type HostApprovalMode } from './hostApprovalMode'

const props = defineProps<{ sessionId: string }>()
const { t } = useI18n()
const mode = ref<HostApprovalMode>('ask')
const saving = ref(false)
const open = ref(false)
let generation = 0
const knownModes = new Map<string, HostApprovalMode>()

const MODE_ICONS: Record<HostApprovalMode, string> = {
  ask: 'user-checked',
  auto: 'robot',
}

function iconName(value: HostApprovalMode) {
  return MODE_ICONS[value]
}

const label = computed(() => t(`input.hostApprovalMode.${mode.value}`))
const hint = computed(() => t(mode.value === 'auto' ? 'input.hostApprovalMode.autoHint' : 'input.hostApprovalMode.askHint'))

async function load(id: string) {
  if (!id) return
  const gen = ++generation
  mode.value = knownModes.get(id) ?? 'ask'
  let responseMode: HostApprovalMode | null = null
  try {
    responseMode = readModeResponse(await getHostApprovalMode(id)).mode
  } catch {
    responseMode = null
  }
  const next = modeAfterLoad(
    {
      sessionId: props.sessionId,
      generation,
      mode: mode.value,
      established: knownModes.has(props.sessionId),
    },
    { sessionId: id, generation: gen, mode: responseMode },
  )
  if (props.sessionId !== id || generation !== gen) return
  mode.value = next
  if (responseMode != null) knownModes.set(id, next)
}

async function choose(value: HostApprovalMode) {
  open.value = false
  const next = parseHostApprovalMode(value)
  if (next === mode.value) return
  const previous = mode.value
  const id = props.sessionId
  const gen = ++generation
  mode.value = next
  knownModes.set(id, next)
  saving.value = true
  try {
    const confirmed = readModeResponse(await setHostApprovalMode(id, next)).mode
    const applied = modeAfterLoad(
      {
        sessionId: props.sessionId,
        generation,
        mode: mode.value,
        established: knownModes.has(props.sessionId),
      },
      { sessionId: id, generation: gen, mode: confirmed },
    )
    if (props.sessionId !== id || generation !== gen) return
    mode.value = applied
    knownModes.set(id, applied)
  } catch {
    if (props.sessionId === id && generation === gen) {
      mode.value = previous
      knownModes.set(id, previous)
    }
    MessagePlugin.error(t('input.hostApprovalMode.switchFailed'))
  } finally {
    saving.value = false
  }
}

watch(() => props.sessionId, (id) => { void load(id) }, { immediate: true })
</script>

<style scoped lang="less">
.host-approval-mode {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 28px;
  padding: 0 10px;
  border: .5px solid var(--td-component-border);
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-weight: 500;
  cursor: pointer;
  flex-shrink: 0;
  user-select: none;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer-hover);
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
}

.host-approval-mode__icon {
  flex-shrink: 0;
  color: currentColor;
}

.host-approval-mode__name {
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}

.host-approval-mode__option-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;

  :deep(.t-icon) {
    flex-shrink: 0;
    color: currentColor;
  }
}

.host-approval-mode__arrow {
  width: 10px;
  height: 10px;
  margin-left: 2px;
  color: currentColor;
  flex-shrink: 0;
  transition: transform var(--app-motion-instant);

  &.rotate {
    transform: rotate(180deg);
  }
}

.host-approval-mode__menu {
  min-width: 148px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.host-approval-mode__option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 30px;
  padding: 4px 8px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 20px;
  text-align: left;
  cursor: pointer;
  white-space: nowrap;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer-hover);
  }

  &.selected {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:disabled {
    cursor: not-allowed;
  }
}
</style>
