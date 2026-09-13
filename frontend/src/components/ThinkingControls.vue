<!--
  ThinkingControls — 共享思考控件（design §8.1.1，能力声明驱动，双形态）。

  editMode:
    'single' — 档位单选（智能体编辑器 / 对话页会话级覆盖）。level 空 = 跟随模型默认。
    'levels' — 允许集多选 + 默认档（模型配置页 ModelEditorDialog 专用形态，
               行为与抽取前的内联实现逐项一致：开关按 can_disable 显隐、
               多选 options = 厂商枚举、默认档 options = 已勾子集回落厂商枚举、
               勾选子集后默认档若不在子集内自动清空）。

  内部规则（design §8.1.5）：
    !caps.Supported          → 整块置灰 + 说明（已存值由调用侧保留，不在此清除）
    CanDisable === false     → single 形态：开关锁定为开且禁用；levels 形态：开关不渲染（v1 行为）
    single 档位 options      → (chatShard.selected_levels ?? caps.SupportedLevels) ∩ caps.SupportedLevels
    门控（chat 类型 && remote 来源）属页面逻辑，留在调用侧。
-->
<template>
  <div class="thinking-controls">
    <!-- 不支持思考：置灰说明 -->
    <p v-if="!supported" class="thinking-controls__hint">
      {{ t('thinking.unsupportedHint') }}
    </p>

    <!-- 支持思考但厂商未声明任何档位（levels 形态 v1 文案） -->
    <p v-else-if="providerLevels.length === 0" class="thinking-controls__hint">
      {{ t('model.editor.thinkingLevelsUnsupportedHint') }}
    </p>

    <template v-else>
      <!-- 思考开关：标题与其余字段标题同款（__label，上下分布） -->
      <div v-if="showToggle" class="thinking-controls__field">
        <label class="thinking-controls__label">{{ t('model.editor.thinkingToggleLabel') }}</label>
        <div class="thinking-controls__toggle">
          <t-switch
            :model-value="effectiveEnabled"
            :disabled="!canDisable"
            @change="(v: unknown) => setEnabled(v === true)"
          />
          <span class="thinking-controls__toggle-desc">{{ t('model.editor.thinkingToggleDesc') }}</span>
        </div>
      </div>

      <!-- single：档位单选，空 = 跟随模型默认（思考未开启不展示，2026-09-13 反馈 #4） -->
      <div v-if="editMode === 'single' && (!showToggle || effectiveEnabled)" class="thinking-controls__field">
        <t-select
          :model-value="modelValue.level ?? ''"
          clearable
          :options="singleLevelOptions"
          :placeholder="t('thinking.levelPlaceholder')"
          @change="(v: unknown) => setLevel(String(v ?? ''))"
        />
      </div>

      <!-- levels：允许集多选 + 默认档（思考未开启不展示——档位与默认档对
           关闭思考的模型无意义；showToggle=false 的宿主面板不受影响） -->
      <template v-else-if="!showToggle || effectiveEnabled">
        <div class="thinking-controls__field">
          <label class="thinking-controls__label">
            {{ t('model.editor.selectedLevelsLabel') }}
            <slot name="badge" :field="'selectedLevels'" />
          </label>
          <t-select
            :model-value="modelValue.selectedLevels ?? []"
            multiple
            clearable
            :min-collapsed-num="4"
            :options="providerLevelOptions"
            :placeholder="t('model.editor.selectedLevelsPlaceholder')"
            @change="onSelectedLevelsChange"
          />
          <p class="thinking-controls__desc">{{ t('model.editor.selectedLevelsDesc') }}</p>
        </div>

        <div class="thinking-controls__field">
          <label class="thinking-controls__label">
            {{ t('model.editor.thinkingLevelLabel') }}
            <slot name="badge" :field="'level'" />
          </label>
          <t-select
            :model-value="modelValue.level ?? ''"
            clearable
            :options="defaultLevelOptions"
            :placeholder="t('model.editor.thinkingLevelPlaceholder')"
            @change="(v: unknown) => { emit('manual', 'level'); setLevel(String(v ?? '')) }"
          />
          <p class="thinking-controls__desc">{{ t('model.editor.thinkingLevelDesc') }}</p>
        </div>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ProviderThinkingCaps } from '@/api/initialization'

export interface ThinkingControlsValue {
  /** single/levels 共用：思考开关（levels 形态无开关的强制思考模型忽略此值） */
  enabled?: boolean
  /** single：当前档位（空 = 跟随模型默认）；levels：默认档 */
  level?: string
  /** levels：允许档位子集 */
  selectedLevels?: string[]
}

/** 所选模型记录的 chat 分片中与本控件相关的字段。 */
export interface ThinkingShard {
  selected_levels?: string[]
}

interface Props {
  editMode?: 'single' | 'levels'
  /** 厂商思考能力声明（/models/providers 下发）；undefined 视为不支持。 */
  caps?: ProviderThinkingCaps
  /** 所选模型记录的 chat 分片（模型 API 已透出），用于档位交集。 */
  chatShard?: ThinkingShard
  modelValue: ThinkingControlsValue
}

interface Emits {
  (e: 'update:modelValue', value: ThinkingControlsValue): void
  /** 用户手动改动某字段时通知调用侧（模型配置页预填来源标注清除用）。 */
  (e: 'manual', field: 'enabled' | 'selectedLevels' | 'level'): void
}

const props = withDefaults(defineProps<Props>(), {
  editMode: 'single',
  caps: undefined,
  chatShard: undefined,
})
const emit = defineEmits<Emits>()
const { t, te } = useI18n()

const supported = computed(() => props.caps?.supported === true)
const canDisable = computed(() => props.caps?.can_disable !== false)

/** levels 形态沿用 v1：强制思考模型不渲染开关；single 形态渲染锁定开关。 */
const showToggle = computed(() => (props.editMode === 'levels' ? canDisable.value : true))

const effectiveEnabled = computed(() =>
  props.editMode === 'single' && !canDisable.value ? true : props.modelValue.enabled === true,
)

const levelLabel = (level: string) => {
  const key = `model.editor.thinkingLevels.${level}`
  return te(key) ? t(key) : level
}

const providerLevels = computed(() => props.caps?.supported_levels ?? [])

const toOptions = (levels: string[]) => levels.map(v => ({ label: levelLabel(v), value: v }))

const providerLevelOptions = computed(() => toOptions(providerLevels.value))

/** single 档位 options = (模型分片子集 ?? 厂商枚举) ∩ 厂商枚举。 */
const singleLevelOptions = computed(() => {
  const shard = props.chatShard?.selected_levels ?? []
  const values = shard.length ? providerLevels.value.filter(l => shard.includes(l)) : providerLevels.value
  return toOptions(values)
})

/** levels 默认档 options：优先已勾子集，未勾选时回落厂商枚举（v1 行为）。 */
const defaultLevelOptions = computed(() => {
  const selected = props.modelValue.selectedLevels ?? []
  return toOptions(selected.length ? selected : providerLevels.value)
})

const emitValue = (patch: Partial<ThinkingControlsValue>) => {
  emit('update:modelValue', { ...props.modelValue, ...patch })
}

const setEnabled = (v: boolean) => {
  emit('manual', 'enabled')
  emitValue({ enabled: v })
}

const setLevel = (v: string) => {
  emitValue({ level: v })
}

/** 勾选档位子集后：默认档若不在子集内则清空（子集是默认档的选项来源，v1 规则）。 */
const onSelectedLevelsChange = (value: unknown) => {
  emit('manual', 'selectedLevels')
  const levels = Array.isArray(value) ? value.map(String) : []
  const current = props.modelValue.level
  if (current && !levels.includes(current)) {
    emitValue({ selectedLevels: levels, level: '' })
  } else {
    emitValue({ selectedLevels: levels })
  }
}
</script>

<style lang="less" scoped>
.thinking-controls {
  display: flex;
  flex-direction: column;
  gap: 12px;

  &__hint {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }

  &__toggle {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  &__toggle-desc {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  &__field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  &__label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  &__desc {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-placeholder);
  }
}
</style>
