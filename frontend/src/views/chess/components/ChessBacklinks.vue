<template>
  <div v-if="links.length" class="chess-backlinks">
    <div class="cbl-title">{{ t('chess.ref.backlinks') }}</div>
    <a
      v-for="(b, i) in links"
      :key="i"
      href="#"
      class="cbl-item"
      :class="classFor(b.source_type)"
      :title="t('chess.ref.openBacklink')"
      @click.prevent="open(b)"
    >
      <t-icon :name="iconFor(b.source_type)" size="14px" />
      {{ b.page_title || b.page_slug }}
    </a>
  </div>
  <div v-else-if="showEmpty && loaded" class="chess-backlinks">
    <div class="cbl-title">{{ t('chess.ref.backlinks') }}</div>
    <div class="cbl-empty">{{ t('chess.ref.noBacklinks') }}</div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { resolveChessBacklinks, type ChessBacklink, type ChessRefType } from '@/utils/chessRef';

// Hiển thị "Được liên kết bởi" cho một đối tượng cờ + bấm để về nguồn.
// Nguồn 'lesson' → mở bài giảng trong Khóa học; 'position' → mở thế cờ trong
// Ngân hàng thế cờ; 'wiki' (mặc định) → mở trang wiki trong KB.
const props = defineProps<{ refType: ChessRefType; slug?: string; showEmpty?: boolean }>();
const emit = defineEmits<{ (e: 'navigate'): void }>();
const { t } = useI18n();
const router = useRouter();

const links = ref<ChessBacklink[]>([]);
const loaded = ref(false);

async function load() {
  links.value = [];
  loaded.value = false;
  if (!props.slug) return;
  links.value = await resolveChessBacklinks(`${props.refType}/${props.slug}`);
  loaded.value = true;
}

// Nhánh RÕ RÀNG cho từng nguồn (không phải nhị phân lesson/else) — nguồn
// 'position'/'chapter' KHÔNG được rơi vào nhánh wiki (chúng có kb_id='' nên sẽ
// điều hướng tới KB rỗng, hỏng route mà không báo lỗi).
function iconFor(sourceType?: string): string {
  if (sourceType === 'lesson') return 'books';
  if (sourceType === 'position') return 'grid';
  if (sourceType === 'chapter') return 'book';
  return 'file';
}
function classFor(sourceType?: string): string {
  if (sourceType === 'lesson') return 'cbl-lesson';
  if (sourceType === 'position') return 'cbl-position';
  if (sourceType === 'chapter') return 'cbl-chapter';
  return 'cbl-wiki';
}
function open(b: ChessBacklink) {
  if (b.source_type === 'lesson') {
    router.push({ name: 'chessCourses', query: { ref: `lesson/${b.page_slug}` } });
  } else if (b.source_type === 'position') {
    router.push({ name: 'chessCourses', query: { ref: `position/${b.page_slug}` } });
  } else if (b.source_type === 'chapter') {
    router.push({ name: 'chessCourses', query: { ref: `chapter/${b.page_slug}` } });
  } else {
    router.push({ name: 'knowledgeBaseDetail', params: { kbId: b.kb_id }, query: { tab: 'wiki', slug: b.page_slug } });
  }
  emit('navigate');
}

watch(() => [props.refType, props.slug] as const, load, { immediate: true });
</script>

<style lang="less" scoped>
.chess-backlinks { margin-top: 12px; }
.cbl-title { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 6px; }
.cbl-item {
  display: inline-flex; align-items: center; gap: 4px;
  margin: 0 6px 6px 0; padding: 2px 8px; font-size: 12px; border-radius: 4px;
  text-decoration: none; cursor: pointer;
  background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary);
  &:hover { background: var(--td-bg-color-container-hover); color: var(--td-brand-color); }
}
.cbl-empty { font-size: 12px; color: var(--td-text-color-placeholder); }
</style>
