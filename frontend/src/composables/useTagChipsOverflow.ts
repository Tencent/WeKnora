import { onBeforeUnmount, reactive } from 'vue';

const TAG_EST_WIDTH = 82;
const TAG_OVERFLOW_MIN = 32;

export function useTagChipsOverflow(datasetKey: 'tagItemId' | 'listTagItemId') {
  const tagVisibleLimit = reactive<Record<string, number>>({});
  const tagItemTotalMap = new Map<string, number>();
  const observedElements = new WeakSet<Element>();
  let tagChipsRO: ResizeObserver | null = null;

  const computeLimit = (width: number, total: number) => {
    if (total <= 0) return 99;
    const maxFit = Math.floor((width - TAG_OVERFLOW_MIN) / TAG_EST_WIDTH);
    const limit = Math.max(1, Math.min(maxFit, total));
    return limit >= total ? 99 : limit;
  };

  const ensureObserver = () => {
    if (tagChipsRO) return;
    tagChipsRO = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const target = entry.target as HTMLElement;
        const id = target.dataset[datasetKey];
        if (!id) continue;
        const total = tagItemTotalMap.get(id) ?? 0;
        tagVisibleLimit[id] = computeLimit(entry.contentRect.width, total);
      }
    });
  };

  function setupTagChipsObserver(el: Element | null, itemId: string, totalCount: number) {
    if (!el) return;
    const htmlEl = el as HTMLElement;
    htmlEl.dataset[datasetKey] = itemId;
    tagItemTotalMap.set(itemId, totalCount);
    ensureObserver();
    if (!observedElements.has(htmlEl)) {
      observedElements.add(htmlEl);
      tagChipsRO!.observe(htmlEl);
    }
    requestAnimationFrame(() => {
      tagVisibleLimit[itemId] = computeLimit(htmlEl.clientWidth, totalCount);
    });
  }

  function getTagLimit(itemId: string): number {
    return tagVisibleLimit[itemId] ?? 99;
  }

  function hasTagOverflow(itemId: string, total: number): boolean {
    return total > getTagLimit(itemId);
  }

  function getOverflowCount(itemId: string, total: number): number {
    return Math.max(0, total - getTagLimit(itemId));
  }

  onBeforeUnmount(() => {
    tagChipsRO?.disconnect();
    tagChipsRO = null;
  });

  return {
    setupTagChipsObserver,
    getTagLimit,
    hasTagOverflow,
    getOverflowCount,
  };
}
