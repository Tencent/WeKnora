import { ref, type Ref } from 'vue';
import type {
  DocumentFolderListResponse,
  DocumentFolderNode,
} from '@/api/knowledge-base';

type FolderPageFetcher = (
  knowledgeBaseId: string,
  parentId: string,
  options?: { keyword?: string; cursor?: string; page_size?: number },
) => Promise<DocumentFolderListResponse>;

interface FolderPageCacheEntry<T> {
  loadedAt: number;
  items: T[];
  nextCursor: string;
  hasMore: boolean;
}

interface UseDocumentFolderPagesOptions<T extends { id: string }> {
  knowledgeBaseId: () => string;
  parentId?: () => string;
  keyword?: () => string;
  mapFolder: (
    folder: DocumentFolderNode,
    context: { knowledgeBaseId: string; parentId: string },
  ) => T;
  errorMessage: (error: unknown) => string;
  pageSize?: number;
  cacheTtlMs?: number;
  clearItemsOnError?: boolean;
  fetchPage: FolderPageFetcher;
}

interface LoadDocumentFolderPageOptions {
  append?: boolean;
  force?: boolean;
  parentId?: string;
}

export function useDocumentFolderPages<T extends { id: string }>(
  options: UseDocumentFolderPagesOptions<T>,
) {
  const items = ref<T[]>([]) as Ref<T[]>;
  const loading = ref(false);
  const error = ref('');
  const nextCursor = ref('');
  const hasMore = ref(false);
  const cache = new Map<string, FolderPageCacheEntry<T>>();
  const pageSize = options.pageSize ?? 50;
  const cacheTtlMs = options.cacheTtlMs ?? 0;
  let generation = 0;

  function reset(clearCache = false) {
    generation += 1;
    items.value = [];
    nextCursor.value = '';
    hasMore.value = false;
    loading.value = false;
    error.value = '';
    if (clearCache) cache.clear();
  }

  function clearCache() {
    cache.clear();
  }

  async function load(loadOptions: LoadDocumentFolderPageOptions = {}) {
    const knowledgeBaseId = options.knowledgeBaseId();
    if (!knowledgeBaseId) {
      reset();
      return;
    }

    const append = loadOptions.append === true;
    const force = loadOptions.force === true;
    const parentId = loadOptions.parentId ?? options.parentId?.() ?? '';
    const keyword = options.keyword?.().trim() || '';
    const cacheKey = `${knowledgeBaseId}\u0000${parentId}\u0000${keyword}`;
    const cached = cache.get(cacheKey);
    if (
      !append
      && !force
      && cacheTtlMs > 0
      && cached
      && Date.now() - cached.loadedAt < cacheTtlMs
    ) {
      items.value = cached.items.map(item => ({ ...item }));
      nextCursor.value = cached.nextCursor;
      hasMore.value = cached.hasMore;
      loading.value = false;
      error.value = '';
      return;
    }

    const requestGeneration = ++generation;
    loading.value = true;
    error.value = '';
    try {
      const response = await options.fetchPage(knowledgeBaseId, parentId, {
        keyword: keyword || undefined,
        cursor: append ? nextCursor.value : undefined,
        page_size: pageSize,
      });
      if (requestGeneration !== generation) return;

      const pageItems = (response?.folders ?? []).map(folder => (
        options.mapFolder(folder, { knowledgeBaseId, parentId })
      ));
      if (append) {
        const knownIds = new Set(items.value.map(item => item.id));
        items.value = [
          ...items.value,
          ...pageItems.filter(item => !knownIds.has(item.id)),
        ];
      } else {
        items.value = pageItems;
      }
      nextCursor.value = response?.next_cursor || '';
      hasMore.value = Boolean(response?.has_more && nextCursor.value);
      if (cacheTtlMs > 0) {
        cache.set(cacheKey, {
          loadedAt: Date.now(),
          items: items.value.map(item => ({ ...item })),
          nextCursor: nextCursor.value,
          hasMore: hasMore.value,
        });
      }
    } catch (loadError: unknown) {
      if (requestGeneration !== generation) return;
      if (!append && options.clearItemsOnError) items.value = [];
      error.value = options.errorMessage(loadError);
    } finally {
      if (requestGeneration === generation) loading.value = false;
    }
  }

  return {
    items,
    loading,
    error,
    nextCursor,
    hasMore,
    load,
    reset,
    clearCache,
  };
}
