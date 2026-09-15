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
    SupportedLevels 空       → 布尔思考（如 ollama think）：levels 形态 = 开关 + 档位提示；
                               single 形态 = 纯开关（档位控件不渲染）
    single 档位 options      → (chatShard.selected_levels ?? caps.SupportedLevels) ∩ caps.SupportedLevels
    门控（chat 类型 && remote 来源）属页面逻辑，留在调用侧。
-->
<template>
  <div class="thinking-controls">
    <!-- 不支持思考：置灰说明 -->
    <p v-if="!supported" class="thinking-controls__hint">
      {{ t('thinking.unsupportedHint') }}
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

      <!-- 档位区随思考开关折叠（2026-09-13 反馈 #4）。grid-rows 0fr/1fr
           过渡做平滑开合——直接 v-if 卸载会让抽屉高度突变，观感像回弹 bug。
           showToggle=false 的宿主面板（无开关）恒展开。 -->
      <div
        class="thinking-controls__collapse"
        :class="{ 'is-collapsed': showToggle && !effectiveEnabled }"
      >
        <div class="thinking-controls__collapse-inner">
          <!-- levels 形态但厂商未声明任何档位（布尔思考如 ollama think）：
               开关有效，档位控件以提示替代（v1 是整块只剩提示、连开关都没有）。 -->
          <p v-if="editMode === 'levels' && providerLevels.length === 0" class="thinking-controls__hint">
            {{ t('model.editor.thinkingLevelsUnsupportedHint') }}
          </p>

          <!-- single：档位单选，空 = 跟随模型默认；厂商未声明档位时不渲染 -->
          <div v-else-if="editMode === 'single' && providerLevels.length > 0" class="thinking-controls__field">
            <t-select
              :model-value="modelValue.level ?? ''"
              clearable
              :options="singleLevelOptions"
              :placeholder="t('thinking.levelPlaceholder')"
              @change="(v: unknown) => setLevel(String(v ?? ''))"
            />
          </div>

          <!-- levels：允许集多选 + 默认档 -->
          <template v-else-if="editMode === 'levels'">
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
        </div>
      </div>
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

/** levels 形态沿用 v1：强制思考模型不渲染开关；single 形态渲染开关（会话
    API 已支持 thinking 布尔覆盖，开关可传输）。 */
const showToggle = computed(() =>
  props.editMode === 'levels' ? canDisable.value : true)

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

  // 档位区折叠容器：grid-rows 1fr/0fr 过渡实现高度动画（内容保持挂载）
  &__collapse {
    display: grid;
    grid-template-rows: 1fr;
    transition: grid-template-rows 0.2s ease;

    &.is-collapsed {
      grid-template-rows: 0fr;
      visibility: hidden;
      // visibility 延迟到收起动画结束后再生效，展开时立即恢复
      transition: grid-template-rows 0.2s ease, visibility 0s 0.2s;
    }
  }

  &__collapse-inner {
    min-height: 0;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    gap: 12px;
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
