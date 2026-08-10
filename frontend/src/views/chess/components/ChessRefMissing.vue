<template>
  <div class="crm">
    <div class="crm-msg">{{ t('chess.ref.notFound', { ref: refStr }) }}</div>
    <div v-if="loading" class="crm-hint">{{ t('chess.ref.loading') }}</div>
    <div v-else-if="suggestions.length" class="crm-suggest">
      <div class="crm-suggest-title">{{ t('chess.ref.didYouMean') }}</div>
      <div class="crm-chips">
        <button
          v-for="s in suggestions"
          :key="s.ref"
          type="button"
          class="crm-chip"
          @click="emit('choose', s.ref)"
        >
          <span class="crm-chip-type" :data-type="s.type">{{ typeLabel(s.type) }}</span>
          <span class="crm-chip-title">{{ s.title }}</span>
        </button>
      </div>
    </div>
    <div class="crm-actions">
      <t-button size="small" variant="outline" @click="createNew">{{ t('chess.ref.createNew') }}</t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { searchChessRefs, type ChessRefSearchItem } from '@/api/chess';
import { parseChessRef } from '@/utils/chessRef';

// Trạng thái "không tìm thấy tham chiếu cờ" thân thiện: gợi ý các mục gần đúng
// (Ý bạn là…?) + nút tạo mới. Dùng chung cho ChessRefDialog và ChessRefEmbed.
const props = defineProps<{ refStr: string }>();
const emit = defineEmits<{ (e: 'choose', ref: string): void }>();
const { t } = useI18n();
const router = useRouter();

const TYPE_LABELS: Record<string, string> = {
  game: 'Ván', puzzle: 'Bài tập', lesson: 'Bài', course: 'Khóa', position: 'Thế cờ',
};
function typeLabel(tp: string): string { return TYPE_LABELS[tp] || tp; }

const loading = ref(false);
const suggestions = ref<ChessRefSearchItem[]>([]);

async function loadSuggestions() {
  suggestions.value = [];
  const parsed = parseChessRef(props.refStr);
  if (!parsed) return;
  loading.value = true;
  try {
    // Tìm theo phần slug đã gõ, giới hạn cùng loại để gợi ý sát hơn.
    const res: any = await searchChessRefs(parsed.slug.replace(/-/g, ' '), { type: parsed.type, limit: 6 });
    suggestions.value = (res?.data || []).filter((s: ChessRefSearchItem) => s.ref !== props.refStr);
  } catch {
    suggestions.value = [];
  } finally {
    loading.value = false;
  }
}

function createNew() {
  const parsed = parseChessRef(props.refStr);
  // Điều hướng tới hub quản lý cờ, mở đúng tab theo loại (ref=<type>/ chuyển tab).
  const type = parsed?.type || 'game';
  router.push({ name: 'chessCourses', query: { ref: `${type}/` } });
}

watch(() => props.refStr, loadSuggestions, { immediate: true });
</script>

<style scoped lang="less">
.crm { padding: 4px 0; }
.crm-msg { color: var(--td-warning-color); font-size: 13px; margin-bottom: 8px; }
.crm-hint { color: var(--td-text-color-placeholder); font-size: 12px; }
.crm-suggest-title { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 6px; }
.crm-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.crm-chip {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 4px 8px; border-radius: 6px; cursor: pointer; font-size: 13px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  background: var(--td-bg-color-container, #fff);
  &:hover { border-color: var(--td-brand-color); background: var(--td-bg-color-container-hover, #f3f3f3); }
}
.crm-chip-type {
  font-size: 11px; font-weight: 600; padding: 0 6px; border-radius: 10px; color: #fff;
  background: var(--td-brand-color, #0052d9);
  &[data-type='puzzle'] { background: #d4380d; }
  &[data-type='lesson'] { background: #389e0d; }
  &[data-type='course'] { background: #531dab; }
  &[data-type='position'] { background: #0a7d6f; }
}
.crm-chip-title { max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.crm-actions { margin-top: 10px; }
</style>
