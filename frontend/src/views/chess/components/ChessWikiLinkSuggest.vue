<template>
  <!-- Dropdown gợi ý tham chiếu cờ khi gõ "[[" trong một <textarea>. Teleport ra
       body để không bị overflow của dialog cắt mất; định vị ngay tại con trỏ. -->
  <teleport to="body">
    <div
      v-if="visible && items.length"
      ref="dropdownEl"
      class="cwl-suggest"
      :style="posStyle"
      @mousedown.prevent
    >
      <div
        v-for="(it, i) in items"
        :key="it.ref"
        class="cwl-item"
        :class="{ active: i === activeIndex }"
        @mouseenter="activeIndex = i"
        @click="choose(it)"
      >
        <span class="cwl-type" :data-type="it.type">{{ typeLabel(it.type) }}</span>
        <span class="cwl-title">{{ it.title }}</span>
        <span v-if="it.subtitle" class="cwl-sub">{{ it.subtitle }}</span>
      </div>
      <div class="cwl-hint">↑↓ chọn · Enter chèn · Esc đóng</div>
    </div>
  </teleport>
</template>

<script setup lang="ts">
import { ref, onBeforeUnmount, watch, nextTick } from 'vue';
import { searchChessRefs, type ChessRefSearchItem } from '@/api/chess';

// Component KHÔNG render textarea — chỉ gắn lắng nghe vào một <textarea> có sẵn và
// hiện dropdown. Host truyền phần tử textarea native + v-model nội dung.
const props = defineProps<{
  textarea: HTMLTextAreaElement | null;
  modelValue: string;
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>();

const REF_TYPES = ['game', 'puzzle', 'lesson', 'course', 'position'];
const TYPE_LABELS: Record<string, string> = {
  game: 'Ván', puzzle: 'Bài tập', lesson: 'Bài', course: 'Khóa', position: 'Thế cờ',
};
function typeLabel(t: string): string { return TYPE_LABELS[t] || t; }

const visible = ref(false);
const items = ref<ChessRefSearchItem[]>([]);
const activeIndex = ref(0);
const posStyle = ref<Record<string, string>>({});
const dropdownEl = ref<HTMLElement | null>(null);

// Vị trí (trong value) của "[[" đang mở và của con trỏ — để biết đoạn cần thay.
let replaceStart = 0;
let caretPos = 0;
let searchTimer: ReturnType<typeof setTimeout> | null = null;
let reqSeq = 0;

function hide() {
  visible.value = false;
  items.value = [];
}

// Phát hiện "[[" chưa đóng ngay trước con trỏ và bóc (type/, query) để tìm gợi ý.
function update() {
  const ta = props.textarea;
  if (!ta) return hide();
  const caret = ta.selectionStart ?? 0;
  const before = ta.value.slice(0, caret);
  // Khớp "[[" mở gần nhất: không chứa [ ] xuống dòng hoặc | (đã sang phần nhãn).
  const m = /\[\[([^[\]\n|]*)$/.exec(before);
  if (!m) return hide();
  const raw = m[1];
  let type = '';
  let q = raw;
  const slash = raw.indexOf('/');
  if (slash >= 0) {
    const head = raw.slice(0, slash).toLowerCase();
    if (REF_TYPES.includes(head)) {
      type = head;
      q = raw.slice(slash + 1);
    }
  }
  replaceStart = caret - m[0].length; // vị trí của "[["
  caretPos = caret;
  positionAt(ta, caret);
  doSearch(q.trim(), type);
}

function doSearch(q: string, type: string) {
  if (searchTimer) clearTimeout(searchTimer);
  const seq = ++reqSeq;
  searchTimer = setTimeout(async () => {
    try {
      const res: any = await searchChessRefs(q, { type, limit: 6 });
      if (seq !== reqSeq) return; // kết quả cũ → bỏ
      items.value = res?.data || [];
      activeIndex.value = 0;
      visible.value = items.value.length > 0;
    } catch {
      hide();
    }
  }, 140);
}

function choose(it: ChessRefSearchItem) {
  const ta = props.textarea;
  if (!ta) return;
  const v = ta.value;
  const link = `[[${it.ref}|${it.title}]]`;
  const next = v.slice(0, replaceStart) + link + v.slice(caretPos);
  emit('update:modelValue', next);
  hide();
  nextTick(() => {
    ta.focus();
    const pos = replaceStart + link.length;
    ta.setSelectionRange(pos, pos);
  });
}

// ---- Điều khiển bàn phím & sự kiện textarea ----
function onKeydown(e: KeyboardEvent) {
  if (!visible.value || !items.value.length) return;
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    activeIndex.value = (activeIndex.value + 1) % items.value.length;
  } else if (e.key === 'ArrowUp') {
    e.preventDefault();
    activeIndex.value = (activeIndex.value - 1 + items.value.length) % items.value.length;
  } else if (e.key === 'Enter' || e.key === 'Tab') {
    e.preventDefault();
    choose(items.value[activeIndex.value]);
  } else if (e.key === 'Escape') {
    e.preventDefault();
    hide();
  }
}
function onInput() { update(); }
function onClick() { update(); }
function onKeyup(e: KeyboardEvent) {
  // Phím điều hướng/chọn đã xử lý ở keydown; chỉ cập nhật cho các phím còn lại.
  if (['ArrowDown', 'ArrowUp', 'Enter', 'Tab', 'Escape'].includes(e.key)) return;
  update();
}
function onBlur() { setTimeout(hide, 120); }
function onScrollOrResize() { if (visible.value) hide(); }

function bind(ta: HTMLTextAreaElement) {
  ta.addEventListener('input', onInput);
  ta.addEventListener('keydown', onKeydown);
  ta.addEventListener('keyup', onKeyup);
  ta.addEventListener('click', onClick);
  ta.addEventListener('blur', onBlur);
  window.addEventListener('scroll', onScrollOrResize, true);
  window.addEventListener('resize', onScrollOrResize);
}
function unbind(ta: HTMLTextAreaElement) {
  ta.removeEventListener('input', onInput);
  ta.removeEventListener('keydown', onKeydown);
  ta.removeEventListener('keyup', onKeyup);
  ta.removeEventListener('click', onClick);
  ta.removeEventListener('blur', onBlur);
  window.removeEventListener('scroll', onScrollOrResize, true);
  window.removeEventListener('resize', onScrollOrResize);
}

watch(() => props.textarea, (el, old) => {
  if (old) unbind(old);
  hide();
  if (el) bind(el);
}, { immediate: true });

onBeforeUnmount(() => {
  if (props.textarea) unbind(props.textarea);
  if (searchTimer) clearTimeout(searchTimer);
});

// ---- Định vị dropdown tại con trỏ (kỹ thuật mirror div) ----
function positionAt(ta: HTMLTextAreaElement, caret: number) {
  const coords = caretCoordinates(ta, caret);
  const rect = ta.getBoundingClientRect();
  const top = rect.top + coords.top - ta.scrollTop + coords.height + 2;
  const left = rect.left + coords.left - ta.scrollLeft;
  posStyle.value = {
    top: `${Math.round(top)}px`,
    left: `${Math.round(Math.min(left, window.innerWidth - 320))}px`,
  };
}

// Mirror-div: dựng một div sao chép style của textarea để đo tọa độ con trỏ.
const MIRROR_PROPS = [
  'boxSizing', 'width', 'height', 'overflowX', 'overflowY',
  'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
  'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
  'fontStyle', 'fontVariant', 'fontWeight', 'fontStretch', 'fontSize',
  'fontSizeAdjust', 'lineHeight', 'fontFamily', 'textAlign', 'textTransform',
  'textIndent', 'textDecoration', 'letterSpacing', 'wordSpacing', 'tabSize',
];
function caretCoordinates(el: HTMLTextAreaElement, position: number) {
  const div = document.createElement('div');
  const style = div.style;
  const computed = window.getComputedStyle(el);
  style.whiteSpace = 'pre-wrap';
  style.wordWrap = 'break-word';
  style.position = 'absolute';
  style.visibility = 'hidden';
  for (const prop of MIRROR_PROPS) {
    // @ts-expect-error chỉ số chuỗi vào CSSStyleDeclaration
    style[prop] = computed[prop];
  }
  div.textContent = el.value.slice(0, position);
  const span = document.createElement('span');
  span.textContent = el.value.slice(position) || '.';
  div.appendChild(span);
  document.body.appendChild(div);
  const top = span.offsetTop + parseInt(computed.borderTopWidth || '0', 10);
  const left = span.offsetLeft + parseInt(computed.borderLeftWidth || '0', 10);
  const height = parseInt(computed.lineHeight || computed.fontSize || '16', 10);
  document.body.removeChild(div);
  return { top, left, height };
}
</script>

<style scoped lang="less">
.cwl-suggest {
  position: fixed;
  z-index: 5000;
  min-width: 240px;
  max-width: 320px;
  max-height: 280px;
  overflow-y: auto;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.16);
  padding: 4px;
}
.cwl-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
  &.active { background: var(--td-bg-color-container-hover, #f3f3f3); }
}
.cwl-type {
  flex: none;
  font-size: 11px;
  font-weight: 600;
  padding: 0 6px;
  border-radius: 10px;
  color: #fff;
  background: var(--td-brand-color, #0052d9);
  &[data-type='puzzle'] { background: #d4380d; }
  &[data-type='lesson'] { background: #389e0d; }
  &[data-type='course'] { background: #531dab; }
  &[data-type='position'] { background: #0a7d6f; }
}
.cwl-title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cwl-sub {
  flex: none;
  color: var(--td-text-color-placeholder, #999);
  font-size: 11px;
}
.cwl-hint {
  padding: 4px 8px 2px;
  color: var(--td-text-color-placeholder, #999);
  font-size: 11px;
  border-top: 1px solid var(--td-component-stroke, #eee);
  margin-top: 2px;
}
</style>
