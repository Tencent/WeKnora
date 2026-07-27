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
  // Folder-aware interactive elements: keep the marquee rectangle
  // from starting when the user clicks a folder checkbox, action menu, or
  // inline rename input. The generic element selectors above (button/input/
  // .t-checkbox) already cover most cases; these mirror the document-card
  // selectors for folder cards/rows so marquee ignores the same affordances.
  '.folder-checkbox',
  '.folder-card-menu',
  '.folder-card-menu-item',
  '.folder-row-more-btn',
  '.folder-row-menu',
  '.folder-row-menu-item',
  '.folder-inline-input',
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
  keyOf: (el: HTMLElement) => string | null,
  selectedKeys: Set<string>,
): MarqueeMode {
  const target = e.target;
  if (!(target instanceof Element)) return 'add';
  const itemEl = target.closest<HTMLElement>(itemSelector);
  if (!itemEl) return 'add';
  const key = keyOf(itemEl);
  if (!key) return 'add';
  return selectedKeys.has(key) ? 'subtract' : 'add';
}

export interface UseMarqueeSelectOptions {
  containerRef: Ref<HTMLElement | null>;
  itemSelector: string;
  /**
   * Legacy id-based selection (backward-compatible alias). The id is treated as
   * the selection key, so existing callers keep working unchanged. Either this
   * pair or the typed-key pair below must be supplied.
   */
  selectedIds?: Ref<Set<string>>;
  getItemId?: (el: HTMLElement) => string | null;
  /**
   * Typed-key selection path (preferred for new file-system callers). Keys use
   * the `folder:<id>` / `knowledge:<id>` format produced by
   * `buildRenderedSelectionKeys` (see folderModel.ts), so marquee and
   * Shift-range share the same rendered order the user sees. Takes precedence
   * over `selectedIds`/`getItemId` when both are supplied.
   *
   * The hook does NOT implement Shift-range itself. The page owns
   * Shift+checkbox logic: it calls `buildRenderedSelectionKeys(directFolders,
   * documents)` to obtain folders-then-documents rendered order, finds the
   * anchor/current indices in that array, and writes the resulting keys into
   * `selectedKeys`. The hook only requires that keys returned by `getItemKey`
   * match the keys stored in `selectedKeys` so marquee add/subtract and the
   * start-mode resolution can compare membership correctly.
   */
  selectedKeys?: Ref<Set<string>>;
  getItemKey?: (el: HTMLElement) => string | null;
  enabled?: Ref<boolean>;
  onSelectionStart?: () => void;
  minDragDistance?: number;
}

export function useMarqueeSelect(options: UseMarqueeSelectOptions) {
  const {
    containerRef,
    itemSelector,
    selectedKeys,
    getItemKey,
    selectedIds,
    getItemId,
    enabled,
    onSelectionStart,
    minDragDistance = DEFAULT_MIN_DRAG_PX,
  } = options;

  // Resolve the active selection path. The typed-key path is preferred for new
  // callers; the legacy id path is kept as a backward-compatible alias where
  // the id IS the key. At least one pair must be supplied.
  const keysRef: Ref<Set<string>> | undefined = selectedKeys ?? selectedIds;
  const keyOf: ((el: HTMLElement) => string | null) | undefined = getItemKey ?? getItemId;
  if (!keysRef || !keyOf) {
    throw new Error(
      'useMarqueeSelect: provide either selectedKeys/getItemKey or selectedIds/getItemId',
    );
  }

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

  const collectIntersectingKeys = (): Set<string> => {
    const container = containerRef.value;
    if (!container) return new Set();
    const box = normalizeRect(startClientX, startClientY, currentClientX, currentClientY);
    const keys = new Set<string>();
    container.querySelectorAll<HTMLElement>(itemSelector).forEach((el) => {
      const key = keyOf(el);
      if (!key) return;
      if (rectsIntersect(box, el.getBoundingClientRect())) keys.add(key);
    });
    return keys;
  };

  const applyMarqueeSelection = () => {
    const hit = collectIntersectingKeys();
    const next = new Set(baseSelection);
    if (dragMode === 'subtract') {
      hit.forEach((key) => next.delete(key));
    } else {
      hit.forEach((key) => next.add(key));
    }
    keysRef.value = next;
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

    dragMode = resolveMarqueeModeFromStart(e, itemSelector, keyOf, keysRef.value);
    baseSelection = new Set(keysRef.value);

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
