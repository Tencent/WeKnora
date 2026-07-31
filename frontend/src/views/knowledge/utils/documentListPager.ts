export interface DocumentListPageRequest {
  page: number;
  generation: number;
}

// Coordinates page-1 refreshes with infinite-scroll requests. A new page-1
// load invalidates every older page request and atomically returns pagination
// to page 1, so an obsolete page-2 completion cannot leave loading locked or
// advance the next request to page 3.
export function createDocumentListPager() {
  let page = 1;
  let generation = 0;
  let loadingMore = false;

  function reset() {
    generation += 1;
    page = 1;
    loadingMore = false;
  }

  function beginFirstPage(): DocumentListPageRequest {
    reset();
    return { page, generation };
  }

  function beginNextPage(): DocumentListPageRequest | undefined {
    if (loadingMore) return undefined;
    page += 1;
    loadingMore = true;
    return { page, generation };
  }

  function finishNextPage(request: DocumentListPageRequest) {
    if (request.generation === generation) {
      loadingMore = false;
    }
  }

  return {
    reset,
    beginFirstPage,
    beginNextPage,
    finishNextPage,
    isCurrent: (requestGeneration: number) => requestGeneration === generation,
    currentPage: () => page,
    isLoadingMore: () => loadingMore,
  };
}
