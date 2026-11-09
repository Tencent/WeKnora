export interface EmptyFolderCandidate {
  path: string;
  totalCount: number;
  hasChildren: boolean;
}

export interface DeletedDocumentFolder {
  kbId: string;
  path: string;
  parentPath: string;
}

export type FolderDeleteError = 'notEmpty' | 'missing' | 'forbidden' | 'failed';

export function canDeleteEmptyDocumentFolder(folder: EmptyFolderCandidate | undefined, canEdit: boolean): boolean {
  return !!(canEdit && folder?.path && folder.totalCount === 0 && !folder.hasChildren);
}

export function createEmptyFolderDeleteState() {
  return { pendingPath: '', error: null as FolderDeleteError | null };
}

export function createEmptyFolderDeleteController(
  state: ReturnType<typeof createEmptyFolderDeleteState>,
  deps: {
    currentContext: () => { kbId: string; canEdit: boolean };
    folderForPath: (path: string) => EmptyFolderCandidate | undefined;
    remove: (kbId: string, path: string) => Promise<unknown>;
    onDeleted: (folder: DeletedDocumentFolder) => Promise<void> | void;
  },
) {
  async function remove(path: string): Promise<'ignored' | 'failed' | 'deleted' | 'refreshFailed'> {
    const target = { ...deps.currentContext() };
    if (state.pendingPath || !target.kbId || !canDeleteEmptyDocumentFolder(deps.folderForPath(path), target.canEdit)) return 'ignored';
    const isCurrent = () => {
      const current = deps.currentContext();
      return current.kbId === target.kbId && current.canEdit;
    };
    state.pendingPath = path;
    state.error = null;
    try {
      try {
        await deps.remove(target.kbId, path);
      } catch (error) {
        if (!isCurrent()) return 'ignored';
        const status = (error as { status?: number; response?: { status?: number } })?.status
          ?? (error as { response?: { status?: number } })?.response?.status;
        state.error = status === 409 ? 'notEmpty' : status === 404 ? 'missing' : status === 403 ? 'forbidden' : 'failed';
        return 'failed';
      }
      if (!isCurrent()) return 'ignored';
      const lastSeparator = path.lastIndexOf('/');
      try {
        await deps.onDeleted({ kbId: target.kbId, path, parentPath: lastSeparator < 0 ? '' : path.slice(0, lastSeparator) });
      } catch {
        return isCurrent() ? 'refreshFailed' : 'ignored';
      }
      return isCurrent() ? 'deleted' : 'ignored';
    } finally {
      state.pendingPath = '';
    }
  }
  return { remove };
}
