import { computed, onScopeDispose, reactive, watch } from 'vue';
import type { KnowledgeFolderTree, ListKnowledgeFilesParams } from '../api/knowledge-base';
import type { KnowledgeReadOptions } from '../api/knowledge-base/knowledgeReadOptions';
import { childFolders, folderPathExists } from '../views/knowledge/folderTree';
import { adaptFilePage, adaptFolderTree, browserErrorKind, type BrowserFile, type BrowserKnowledgeBase } from '../utils/mentionFileBrowser';

export interface FileBrowserContext extends KnowledgeReadOptions { key: string }
export interface FileBrowserApi {
  folders(kbId: string, options: KnowledgeReadOptions): Promise<unknown>;
  files(kbId: string, params: ListKnowledgeFilesParams, options: KnowledgeReadOptions): Promise<unknown>;
}

/** A read-only, page-bounded browser. No selection store is available here. */
export function useMentionFileBrowser(context: () => FileBrowserContext, api: FileBrowserApi) {
  const state = reactive({ kb: null as BrowserKnowledgeBase | null, path: '', keyword: '',
    tree: null as KnowledgeFolderTree | null, items: [] as BrowserFile[], page: 0, total: 0,
    hasMore: false, loading: false, error: '' as '' | 'forbidden' | 'missing' | 'error' });
  let generation = 0;
  let pending: AbortController | undefined;
  let requestedPage = 1;
  const invalidate = () => { ++generation; pending?.abort(); pending = undefined; state.loading = false; };
  const reset = () => {
    invalidate();
    Object.assign(state, { kb: null, path: '', keyword: '', tree: null, items: [], page: 0, total: 0, hasMore: false, error: '' });
  };
  watch(() => context().key, reset, { flush: 'sync' });
  onScopeDispose(reset);

  async function load(page = 1) {
    if (!state.kb) return;
    invalidate();
    const seq = generation;
    const controller = new AbortController();
    pending = controller;
    const access = context();
    const options = { agent_id: access.agent_id, agent_source_tenant_id: access.agent_source_tenant_id, signal: controller.signal };
    const kb = { ...state.kb }, path = state.path, keyword = state.keyword.trim();
    const stale = () => generation !== seq || controller.signal.aborted || context().key !== access.key;
    requestedPage = page;
    state.loading = true;
    state.error = '';
    state.items = [];
    try {
      // Refresh the tree on navigation too: a renamed/deleted path must not look like an empty directory.
      const [treeResponse, pageResponse] = await Promise.all([
        api.folders(kb.id, options),
        api.files(kb.id, { page, page_size: 20, folder_path: path, folder_recursive: !!keyword,
          keyword, sort_by: 'file_name', sort_order: 'asc' }, options),
      ]);
      if (stale()) return;
      const tree = adaptFolderTree(treeResponse);
      if (!folderPathExists(tree.folders, path)) { state.tree = null; state.error = 'missing'; return; }
      const result = adaptFilePage(pageResponse, kb);
      if (result.page !== page) throw new Error('Unexpected page');
      // A misbehaving/older backend must never display files outside the requested directory.
      if (result.items.some(file => keyword
        ? path && file.folderPath !== path && !file.folderPath.startsWith(`${path}/`)
        : file.folderPath !== path)) throw new Error('Unexpected folder scope');
      Object.assign(state, { tree, items: result.items, page: result.page, total: result.total, hasMore: result.hasMore });
    } catch (error) {
      if (!stale()) { state.tree = null; state.error = browserErrorKind(error); }
    } finally {
      if (!stale()) state.loading = false;
    }
  }
  const selectKb = (kb: BrowserKnowledgeBase) => { reset(); state.kb = { ...kb }; return load(); };
  const navigate = (path: string) => { state.path = path; state.keyword = ''; state.tree = null; state.page = 0; return load(); };
  const search = (keyword: string) => { state.keyword = keyword; state.page = 0; return load(); };
  const next = () => !state.loading && state.hasMore ? load(state.page + 1) : Promise.resolve();
  const previous = () => !state.loading && state.page > 1 ? load(state.page - 1) : Promise.resolve();
  return { state, folders: computed(() => state.keyword.trim() ? [] : childFolders(state.tree, state.path)),
    selectKb, navigate, search, next, previous, retry: () => load(requestedPage), reset };
}
