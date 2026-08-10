<template>
  <div class="chess-ref-embed">
    <div v-if="loading" class="cre-state">{{ t('chess.ref.loading') }}</div>
    <ChessRefMissing v-else-if="!resolved || !resolved.found" :ref-str="activeRef" @choose="onChoose" />
    <template v-else>
      <div class="cre-head">
        <t-icon :name="iconName" />
        <a href="#" class="cre-title" @click.prevent="openInLibrary">{{ resolved.title }}</a>
        <span class="cre-type">{{ typeLabel }}</span>
      </div>
      <ChessBoardDisplay v-if="resolved.board" :data="resolved.board" />
      <div v-else class="cre-state">{{ t('chess.ref.noBoard') }}</div>
      <ChessBacklinks :ref-type="resolved.type" :slug="resolved.slug" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import ChessBoardDisplay from '@/views/chat/components/tool-results/ChessBoardDisplay.vue';
import ChessBacklinks from './ChessBacklinks.vue';
import ChessRefMissing from './ChessRefMissing.vue';
import { resolveChessRef, type ResolvedChessRef, type ChessRefType } from '@/utils/chessRef';

// Nhúng bàn cờ tương tác inline từ ![[game/<slug>]] trong nội dung wiki/bài giảng.
const props = defineProps<{ refStr: string }>();
const { t } = useI18n();
const router = useRouter();

const loading = ref(true);
const resolved = ref<ResolvedChessRef | null>(null);
// Cho phép chọn gợi ý "Ý bạn là…?" để đổi tham chiếu hiển thị tại chỗ.
const overrideRef = ref('');
const activeRef = computed(() => overrideRef.value || props.refStr);

const iconMap: Record<ChessRefType, string> = {
  game: 'play-circle', puzzle: 'help-circle', lesson: 'books', course: 'folder', position: 'grid',
};
const iconName = computed(() => (resolved.value ? iconMap[resolved.value.type] : 'chess'));
const typeLabel = computed(() => (resolved.value ? t(`chess.ref.type_${resolved.value.type}`) : ''));

async function load() {
  loading.value = true;
  try {
    resolved.value = await resolveChessRef(activeRef.value);
  } catch {
    resolved.value = null;
  } finally {
    loading.value = false;
  }
}

function onChoose(ref: string) {
  overrideRef.value = ref;
  load();
}

function openInLibrary() {
  router.push({ name: 'chessCourses', query: { ref: activeRef.value } });
}

onMounted(load);
watch(() => props.refStr, () => {
  overrideRef.value = '';
  load();
});
</script>

<style lang="less" scoped>
.chess-ref-embed {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 10px 12px;
  margin: 10px 0;
  background: var(--td-bg-color-container);
}
.cre-head {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
  font-size: 13px;
}
.cre-title {
  font-weight: 600;
  color: var(--td-brand-color);
  text-decoration: none;
  &:hover { text-decoration: underline; }
}
.cre-type {
  margin-left: auto;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  background: var(--td-bg-color-secondarycontainer);
  padding: 0 6px;
  border-radius: 4px;
}
.cre-state { color: var(--td-text-color-placeholder); font-size: 13px; padding: 4px 0; }
.cre-missing { color: var(--td-warning-color); }
</style>
