export type FolderCreateError = 'invalid' | 'tooLong' | 'tooDeep' | 'duplicate' | 'parentMissing' | 'forbidden' | 'failed';

export interface FolderCreateContext {
  kbId: string;
  parentPath: string;
  canEdit: boolean;
}

export interface CreatedDocumentFolder {
  kbId: string;
  parentPath: string;
  path: string;
}

export interface FolderCreateState {
  visible: boolean;
  kbId: string;
  parentPath: string;
  name: string;
  pending: boolean;
  error: FolderCreateError | null;
}

export function createDocumentFolderState(): FolderCreateState {
  return { visible: false, kbId: '', parentPath: '', name: '', pending: false, error: null };
}

const byteLength = (value: string) => new TextEncoder().encode(value).length;

/** Keep these byte/depth limits aligned with the server's document-folder rules. */
export function validateDocumentFolderName(rawName: string, parentPath: string, existingPaths: string[]): FolderCreateError | null {
  const name = rawName.trim();
  if (!name || /[\/\\／∕⁄\u0000-\u001f\u007f-\u009f]/u.test(rawName)
    || name === '.' || name === '..' || name.endsWith('.')) return 'invalid';
  const path = parentPath ? `${parentPath}/${name}` : name;
  if (byteLength(name) > 128 || byteLength(path) > 1024) return 'tooLong';
  if (path.split('/').length > 16) return 'tooDeep';
  if (existingPaths.includes(path)) return 'duplicate';
  return null;
}

interface FolderCreateDependencies {
  currentContext: () => FolderCreateContext;
  existingPaths: () => string[];
  create: (kbId: string, payload: { parent_path: string; name: string }) => Promise<{ data?: { path: string } }>;
  onCreated: (folder: CreatedDocumentFolder) => Promise<void> | void;
}

function errorFromResponse(error: unknown): FolderCreateError {
  const response = error as { status?: number; response?: { status?: number } } | undefined;
  switch (response?.status ?? response?.response?.status) {
    case 400: return 'invalid';
    case 403: return 'forbidden';
    case 404: return 'parentMissing';
    case 409: return 'duplicate';
    default: return 'failed';
  }
}

/** Pass a Vue-reactive state object in the component; tests use an ordinary object. */
export function createDocumentFolderController(state: FolderCreateState, deps: FolderCreateDependencies) {
  const isCurrent = (target: Pick<FolderCreateState, 'kbId' | 'parentPath'> = state) => {
    const current = deps.currentContext();
    return current.canEdit && current.kbId === target.kbId && current.parentPath === target.parentPath;
  };

  function open() {
    const current = deps.currentContext();
    if (state.pending || !current.canEdit || !current.kbId) return false;
    Object.assign(state, { visible: true, kbId: current.kbId, parentPath: current.parentPath, name: '', error: null });
    return true;
  }

  function cancel() {
    if (state.pending) return false;
    state.visible = false;
    state.error = null;
    return true;
  }

  function contextChanged() {
    if (!isCurrent()) {
      state.visible = false;
      state.error = null;
    }
  }

  async function submit(): Promise<'ignored' | 'invalid' | 'failed' | 'created' | 'refreshFailed'> {
    if (state.pending || !state.visible) return 'ignored';
    if (!isCurrent()) { contextChanged(); return 'ignored'; }
    state.error = validateDocumentFolderName(state.name, state.parentPath, deps.existingPaths());
    if (state.error) return 'invalid';
    const target = { kbId: state.kbId, parentPath: state.parentPath };
    const name = state.name.trim();
    state.pending = true;
    try {
      let response: { data?: { path: string } };
      try {
        response = await deps.create(target.kbId, { parent_path: target.parentPath, name });
        if (!response?.data?.path) throw new Error('Missing created folder path');
      } catch (error) {
        if (!isCurrent(target) || !state.visible) return 'ignored';
        state.error = errorFromResponse(error);
        return 'failed';
      }
      if (!isCurrent(target) || !state.visible) return 'ignored';
      state.visible = false;
      // The folder already exists once POST succeeds. A refresh failure must
      // never invite the user to submit the same creation again.
      try {
        await deps.onCreated({ ...target, path: response.data!.path });
      } catch {
        return isCurrent(target) ? 'refreshFailed' : 'ignored';
      }
      return isCurrent(target) ? 'created' : 'ignored';
    } finally {
      state.pending = false;
    }
  }

  return { open, cancel, contextChanged, submit };
}
