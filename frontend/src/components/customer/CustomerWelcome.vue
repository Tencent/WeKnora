<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { brandConfig, productLinesFallback } from '@/brand'
import type { ProductLine } from '@/brand/types'
import { useProductLineStore } from '@/stores/productLine'

/**
 * 客户新对话欢迎区：欢迎语 + 产线卡片 + 当前产线示例问题 + 底部提示。
 *
 * 只负责展示与交互，不做发送：示例问题通过 `send` 事件交给 creatChat，
 * 由它调用 Input-field 的 triggerSend，保证和手动输入走同一条发送链路（AC-20）。
 * 产线状态仍然只落在 productLine store，切换只影响界面（AC-18 / C7）。
 */
const emit = defineEmits<{
  send: [question: string]
}>()

const { t } = useI18n()
const productLineStore = useProductLineStore()

const lines = computed(() => productLineStore.lines)
const currentLine = computed(() => productLineStore.currentLine)
const samples = computed(() => currentLine.value?.samples ?? [])

function chooseLine(line: ProductLine): void {
  if (line.id === currentLine.value?.id) return
  productLineStore.selectLine(line.id)
}

function ask(question: string): void {
  if (!question.trim()) return
  emit('send', question)
}
</script>

<template>
  <section class="customer-welcome" aria-labelledby="customer-welcome-title">
    <h1 id="customer-welcome-title" class="customer-welcome__title">
      {{ t('brand.welcome.title', { name: brandConfig.productName }) }}
    </h1>
    <p class="customer-welcome__subtitle">{{ t('brand.welcome.subtitle') }}</p>

    <div class="customer-welcome__lines" role="group" :aria-label="t('brand.welcome.lineGroupLabel')">
      <button v-for="line in lines" :key="line.id" type="button" class="line-card"
        :class="{ 'is-active': line.id === currentLine?.id }" :aria-pressed="line.id === currentLine?.id"
        @click="chooseLine(line)">
        <span class="line-card__name">{{ line.name }}</span>
        <span v-if="line.description" class="line-card__desc">{{ line.description }}</span>
      </button>
    </div>

    <!-- 切产线时用 out-in 过渡让示例问题先淡出再淡入；时长走 --app-motion-instant（120ms） -->
    <Transition name="samples-swap" mode="out-in">
      <div v-if="samples.length > 0" :key="currentLine?.id" class="customer-welcome__samples"
        role="group" :aria-label="t('brand.welcome.samplesLabel')">
        <button v-for="question in samples" :key="question" type="button" class="sample-chip" @click="ask(question)">
          {{ question }}
        </button>
      </div>
    </Transition>

    <!-- 产线配置整段不可用时只提示，不回退成不可用界面（AC-21） -->
    <p v-if="productLinesFallback" class="customer-welcome__notice" role="status">
      {{ t('brand.welcome.fallbackNotice') }}
    </p>

    <p v-if="brandConfig.chatDisclaimer" class="customer-welcome__disclaimer">
      {{ brandConfig.chatDisclaimer }}
    </p>
  </section>
</template>

<style lang="less" scoped>
.customer-welcome {
  width: 100%;
  max-width: 820px;
  margin: 0 auto;
  padding: 0 var(--app-space-4);
  box-sizing: border-box;
  text-align: center;
}

.customer-welcome__title {
  margin: 0 0 var(--app-space-2);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-4xl);
  font-weight: 600;
  line-height: 1.35;
}

.customer-welcome__subtitle {
  margin: 0 0 var(--app-space-6);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
}

.customer-welcome__lines {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--app-space-4);
}

.line-card {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
  width: 320px;
  max-width: 100%;
  padding: var(--app-space-4) var(--app-space-5);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  text-align: left;
  cursor: pointer;
  transition:
    border-color var(--app-motion-fast) ease,
    box-shadow var(--app-motion-fast) ease,
    transform var(--app-motion-fast) ease;
}

.line-card:hover {
  transform: translateY(-1px);
  box-shadow: var(--td-shadow-1);
}

.line-card.is-active {
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--td-brand-color) 14%, transparent);
}

.line-card:focus-visible {
  outline: 2px solid var(--td-brand-color);
  outline-offset: 2px;
}

.line-card__name {
  font-size: var(--app-text-lg);
  font-weight: 600;
}

.line-card__desc {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
}

.customer-welcome__samples {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--app-space-2);
  margin-top: var(--app-space-5);
}

.sample-chip {
  padding: 5px var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  cursor: pointer;
  transition:
    color var(--app-motion-fast) ease,
    border-color var(--app-motion-fast) ease,
    background var(--app-motion-fast) ease;
}

.sample-chip:hover {
  color: var(--td-brand-color);
  border-color: var(--td-brand-color);
  background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
}

.sample-chip:focus-visible {
  outline: 2px solid var(--td-brand-color);
  outline-offset: 2px;
}

.samples-swap-enter-active,
.samples-swap-leave-active {
  transition: opacity var(--app-motion-instant) ease;
}

.samples-swap-enter-from,
.samples-swap-leave-to {
  opacity: 0;
}

.customer-welcome__notice {
  margin: var(--app-space-4) 0 0;
  color: var(--td-warning-color);
  font-size: var(--app-text-sm);
}

.customer-welcome__disclaimer {
  margin: var(--app-space-6) 0 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

@media (prefers-reduced-motion: reduce) {
  .line-card,
  .sample-chip,
  .samples-swap-enter-active,
  .samples-swap-leave-active {
    transition: none;
  }
}
</style>
