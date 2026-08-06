<script setup lang="ts">
import { ref, computed } from 'vue'
import { withBase } from 'vitepress'

const props = defineProps<{
  /** 相对站点根的图片路径，约定放在 public/screenshots/ 下 */
  src: string
  /** 图注：说明这张图展示的是什么 */
  caption: string
  /** 补充说明：截图应覆盖哪些界面元素，供补图的人参考 */
  hint?: string
}>()

// 图片可能尚未补充。默认先渲染占位框，只有确实加载成功才换成图片，
// 这样缺图时不会先闪一个碎图标再降级。
const loaded = ref(false)
const resolved = computed(() => withBase(props.src))
</script>

<template>
  <figure class="wk-shot">
    <img
      v-show="loaded"
      class="wk-shot-img"
      :src="resolved"
      :alt="caption"
      @load="loaded = true"
    />
    <div v-if="!loaded" class="wk-shot-placeholder">
      <div class="wk-shot-placeholder-badge">截图待补充</div>
      <div class="wk-shot-placeholder-caption">{{ caption }}</div>
      <p v-if="hint" class="wk-shot-placeholder-hint">{{ hint }}</p>
      <code class="wk-shot-placeholder-path">website-docs/public{{ src }}</code>
    </div>
    <figcaption class="wk-shot-caption">{{ caption }}</figcaption>
  </figure>
</template>

<style scoped>
.wk-shot {
  margin: 24px 0;
}

.wk-shot-img {
  display: block;
  width: 100%;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
}

.wk-shot-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 32px 24px;
  text-align: center;
  border: 1px dashed var(--vp-c-divider);
  border-radius: 10px;
  background: var(--vp-c-bg-soft);
}

.wk-shot-placeholder-badge {
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  line-height: 20px;
  color: var(--vp-c-text-2);
  background: var(--vp-c-default-soft);
}

.wk-shot-placeholder-caption {
  font-size: 15px;
  font-weight: 600;
  color: var(--vp-c-text-1);
}

.wk-shot-placeholder-hint {
  max-width: 46em;
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
}

.wk-shot-placeholder-path {
  font-size: 12px;
  color: var(--vp-c-text-3);
}

.wk-shot-caption {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
  color: var(--vp-c-text-2);
}
</style>
