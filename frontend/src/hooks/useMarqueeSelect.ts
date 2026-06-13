import { computed, onBeforeUnmount, ref, type Ref } from 'vue';

type Rect = { left: number; top: number; right: number; bottom: number };
/** iOS Photos select-mode marquee: add or subtract for the whole gesture. */
type MarqueeMode = 'add' | 'subtract';

const IGNORE_TARGET_SELECTOR = [
  'button',
  'a',
  'input',
  'textarea',
  'select',
  'label',
  '.t-checkbox',
  '.more-wrap',
  '.card-menu',
  '.card-menu-item',
  '.card-tag-selector',
  '.row-more-btn',
  '.row-menu',
  '.row-menu-item',
  '.doc-list-header',
].join(', ');

const DEFAULT_MIN_DRAG_PX = 6;

function normalizeRect(x1: number, y1: number, x2: number, y2: number): Rect {
  return {
    left: Math.min(x1, x2),
    top: Math.min(y1, y2),
    right: Math.max(x1, x2),
    bottom: Math.max(y1, y2),
  };
}

function rectsIntersect(a: Rect, b: DOMRect): boolean {
  return !(a.right < b.left || a.left > b.right || a.bottom < b.top || a.top > b.bottom);
}

function shouldIgnoreTarget(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return true;
  return !!target.closest(IGNORE_TARGET_SELECTOR);
}

/**
 * iOS Photos「选择」模式框选规则：
 * - 手势起点落在未选中项（或空白区域）→ 本次拖选全程追加选中
 * - 手势起点落在已选中项 → 本次拖选全程取消选中
 */
function resolveMarqueeModeFromStart(
  e: MouseEvent,
  itemSelector: string,
  getItemId: (el: HTMLElement) => string | null,
  selectedIds: Set<string>,
): MarqueeMode {
  const target = e.target;
  if (!(target instanceof Element)) return 'add';
  const itemEl = target.closest<HTMLElement>(itemSelector);
  if (!itemEl) return 'add';
  const id = getItemId(itemEl);
  if (!id) return 'add';
  return selectedIds.has(id) ? 'subtract' : 'add';
}

export interface UseMarqueeSelectOptions {
  containerRef: Ref<HTMLElement | null>;
  itemSelector: string;
  selectedIds: Ref<Set<string>>;
  getItemId: (el: HTMLElement) => string | null;
  enabled?: Ref<boolean>;
  onSelectionStart?: () => void;
  minDragDistance?: number;
}

export function useMarqueeSelect(options: UseMarqueeSelectOptions) {
  const {
    containerRef,
    itemSelector,
    selectedIds,
    getItemId,
    enabled,
    onSelectionStart,
    minDragDistance = DEFAULT_MIN_DRAG_PX,
  } = options;

  const isActive = ref(false);
  const boxVisible = ref(false);
  const boxStyle = ref<Record<string, string>>({});
  const suppressClickUntil = ref(0);
  const marqueeMode = ref<MarqueeMode>('add');

  let startClientX = 0;
  let startClientY = 0;
  let currentClientX = 0;
  let currentClientY = 0;
  let baseSelection = new Set<string>();
  let dragMode: MarqueeMode = 'add';

  const updateBoxStyle = () => {
    const container = containerRef.value;
    if (!container) return;
    const rect = container.getBoundingClientRect();
    const left = Math.min(startClientX, currentClientX) - rect.left + container.scrollLeft;
    const top = Math.min(startClientY, currentClientY) - rect.top + container.scrollTop;
    const width = Math.abs(currentClientX - startClientX);
    const height = Math.abs(currentClientY - startClientY);
    boxStyle.value = {
      left: `${left}px`,
      top: `${top}px`,
      width: `${width}px`,
      height: `${height}px`,
    };
  };

  const collectIntersectingIds = (): Set<string> => {
    const container = containerRef.value;
    if (!container) return new Set();
    const box = normalizeRect(startClientX, startClientY, currentClientX, currentClientY);
    const ids = new Set<string>();
    container.querySelectorAll<HTMLElement>(itemSelector).forEach((el) => {
      const id = getItemId(el);
      if (!id) return;
      if (rectsIntersect(box, el.getBoundingClientRect())) ids.add(id);
    });
    return ids;
  };

  const applyMarqueeSelection = () => {
    const hit = collectIntersectingIds();
    const next = new Set(baseSelection);
    if (dragMode === 'subtract') {
      hit.forEach((id) => next.delete(id));
    } else {
      hit.forEach((id) => next.add(id));
    }
    selectedIds.value = next;
  };

  const endDrag = () => {
    if (!isActive.value) return;
    isActive.value = false;
    boxVisible.value = false;
    marqueeMode.value = 'add';
    document.body.style.removeProperty('user-select');
    document.removeEventListener('mousemove', onDocumentMouseMove);
    document.removeEventListener('mouseup', onDocumentMouseUp);
    if (Math.hypot(currentClientX - startClientX, currentClientY - startClientY) >= minDragDistance) {
      suppressClickUntil.value = Date.now() + 150;
    }
  };

  const onDocumentMouseMove = (e: MouseEvent) => {
    if (!isActive.value) return;
    currentClientX = e.clientX;
    currentClientY = e.clientY;
    const distance = Math.hypot(currentClientX - startClientX, currentClientY - startClientY);
    if (!boxVisible.value && distance >= minDragDistance) {
      boxVisible.value = true;
      marqueeMode.value = dragMode;
      onSelectionStart?.();
    }
    if (boxVisible.value) {
      updateBoxStyle();
      applyMarqueeSelection();
    }
  };

  const onDocumentMouseUp = () => {
    endDrag();
  };

  const onContainerMouseDown = (e: MouseEvent) => {
    if (e.button !== 0) return;
    if (enabled && !enabled.value) return;
    if (shouldIgnoreTarget(e.target)) return;

    const container = containerRef.value;
    if (!container) return;

    dragMode = resolveMarqueeModeFromStart(e, itemSelector, getItemId, selectedIds.value);
    baseSelection = new Set(selectedIds.value);

    isActive.value = true;
    startClientX = e.clientX;
    startClientY = e.clientY;
    currentClientX = e.clientX;
    currentClientY = e.clientY;
    boxVisible.value = false;
    boxStyle.value = {};

    document.body.style.userSelect = 'none';
    document.addEventListener('mousemove', onDocumentMouseMove);
    document.addEventListener('mouseup', onDocumentMouseUp);
  };

  const shouldSuppressClick = () => Date.now() < suppressClickUntil.value;

  const marqueeVisible = computed(() => boxVisible.value);

  onBeforeUnmount(() => {
    endDrag();
  });

  return {
    onContainerMouseDown,
    marqueeVisible,
    marqueeMode,
    boxStyle,
    shouldSuppressClick,
  };
}
